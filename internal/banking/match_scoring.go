package banking

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/npmulder/ledgerly/internal/platform/db"
)

const (
	InvoiceScoreExactNativeAmount       = 0.62
	InvoiceScorePayeeClientMatch        = 0.18
	InvoiceScoreTxnOnOrAfterIssueDate   = 0.05
	InvoiceScoreTxnBeforeIssuePenalty   = -0.03
	InvoiceScoreReferenceContainsBonus  = 0.30
	InvoiceScoreReferenceNearConclusive = 0.95

	InvoiceHighConfidenceThreshold = 0.95
	InvoiceSuggestionThreshold     = 0.60

	PayeeRuleSuggestionConfidence     = 0.72
	DLADirectorNameConfidence         = 0.82
	DLAPersonalPatternConfidence      = 0.70
	DefaultPayeeRuleAutoPostThreshold = 5
)

const dlaSuggestionTarget = "director-loan"

type suggestionDecision struct {
	input     SuggestionInput
	payeeRule *PayeeRule
}

func (s *Service) evaluateTransaction(ctx context.Context, tx db.Tx, txn Transaction) (*suggestionDecision, error) {
	return s.evaluateTransactionWithDirectorNames(ctx, tx, txn, nil)
}

func (s *Service) evaluateTransactionWithDirectorNames(ctx context.Context, tx db.Tx, txn Transaction, directorNames DirectorNameSource) (*suggestionDecision, error) {
	var decisions []suggestionDecision

	if decision, err := s.invoiceMatchDecision(ctx, tx, txn); err != nil {
		return nil, err
	} else if decision != nil {
		decisions = append(decisions, *decision)
	}

	if decision, err := s.payeeRuleDecision(ctx, tx, txn); err != nil {
		return nil, err
	} else if decision != nil {
		decisions = append(decisions, *decision)
	}

	if decision, err := s.dlaDecisionWithDirectorNames(ctx, txn, directorNames); err != nil {
		return nil, err
	} else if decision != nil {
		decisions = append(decisions, *decision)
	}

	if len(decisions) == 0 {
		return nil, nil
	}

	// Priority is product-defined, not score-defined: invoice matches settle
	// receivables, payee rules categorize repeat spend, and DLA is a fallback
	// owner-drawing classifier. BNK-2 only displays one active suggestion.
	slices.SortFunc(decisions, func(a, b suggestionDecision) int {
		ap := suggestionKindPriority(a.input.Kind)
		bp := suggestionKindPriority(b.input.Kind)
		if ap != bp {
			return ap - bp
		}
		if a.input.Confidence != b.input.Confidence {
			if a.input.Confidence > b.input.Confidence {
				return -1
			}
			return 1
		}
		if a.input.Target < b.input.Target {
			return -1
		}
		if a.input.Target > b.input.Target {
			return 1
		}
		return 0
	})
	return &decisions[0], nil
}

func suggestionKindPriority(kind SuggestionKind) int {
	switch kind {
	case SuggestionKindInvoiceMatch:
		return 0
	case SuggestionKindPayeeRule:
		return 1
	case SuggestionKindDLA:
		return 2
	default:
		return 99
	}
}

func (s *Service) invoiceMatchDecision(ctx context.Context, tx db.Tx, txn Transaction) (*suggestionDecision, error) {
	if txn.Amount.Amount <= 0 || s.invoiceCandidates == nil {
		return nil, nil
	}
	candidates, err := s.invoiceCandidates.InvoiceCandidates(ctx, tx, txn.Amount.Currency)
	if err != nil {
		return nil, fmt.Errorf("banking: invoice match candidates: %w", err)
	}
	best, ok := bestInvoiceMatch(txn, candidates)
	if !ok || best.score < InvoiceSuggestionThreshold {
		return nil, nil
	}
	return &suggestionDecision{
		input: SuggestionInput{
			TransactionID: txn.ID,
			Kind:          SuggestionKindInvoiceMatch,
			Confidence:    best.score,
			Target:        best.candidate.InvoiceID,
			Explanation:   invoiceMatchExplanation(best),
		},
	}, nil
}

type invoiceMatchResult struct {
	candidate InvoiceMatchCandidate
	score     float64
	factors   []string
}

func bestInvoiceMatch(txn Transaction, candidates []InvoiceMatchCandidate) (invoiceMatchResult, bool) {
	var best invoiceMatchResult
	found := false
	for _, candidate := range candidates {
		score, factors, ok := scoreInvoiceCandidate(txn, candidate)
		if !ok {
			continue
		}
		current := invoiceMatchResult{candidate: candidate, score: score, factors: factors}
		if !found || compareInvoiceMatch(current, best) < 0 {
			best = current
			found = true
		}
	}
	return best, found
}

func compareInvoiceMatch(a, b invoiceMatchResult) int {
	if a.score != b.score {
		if a.score > b.score {
			return -1
		}
		return 1
	}
	if aStatus, bStatus := invoiceMatchStatusRank(a.candidate.Status), invoiceMatchStatusRank(b.candidate.Status); aStatus != bStatus {
		if aStatus < bStatus {
			return -1
		}
		return 1
	}
	aNumber := strings.TrimSpace(a.candidate.Number)
	bNumber := strings.TrimSpace(b.candidate.Number)
	if aNumber != bNumber {
		if aNumber < bNumber {
			return -1
		}
		return 1
	}
	if a.candidate.InvoiceID < b.candidate.InvoiceID {
		return -1
	}
	if a.candidate.InvoiceID > b.candidate.InvoiceID {
		return 1
	}
	return 0
}

func invoiceMatchStatusRank(status string) int {
	switch strings.TrimSpace(status) {
	case "sent":
		return 0
	case "draft":
		return 1
	default:
		return 2
	}
}

func scoreInvoiceCandidate(txn Transaction, candidate InvoiceMatchCandidate) (float64, []string, bool) {
	if strings.TrimSpace(candidate.InvoiceID) == "" {
		return 0, nil, false
	}
	if status := strings.TrimSpace(candidate.Status); status != "" {
		switch status {
		case "draft", "sent":
		default:
			return 0, nil, false
		}
	}
	if candidate.Settled {
		return 0, nil, false
	}
	if candidate.Amount.Currency != txn.Amount.Currency {
		return 0, nil, false
	}

	var score float64
	var factors []string
	if candidate.Amount.Amount == txn.Amount.Amount {
		score += InvoiceScoreExactNativeAmount
		factors = append(factors, "exact native amount")
	}

	payeeScore := normalizedSimilarity(txn.Payee, candidate.ClientName)
	if payeeScore >= 0.70 {
		score += InvoiceScorePayeeClientMatch * payeeScore
		factors = append(factors, "payee resembles client")
	}

	if !candidate.IssueDate.IsZero() {
		if !scoreDateOnly(txn.Date).Before(scoreDateOnly(candidate.IssueDate)) {
			score += InvoiceScoreTxnOnOrAfterIssueDate
			factors = append(factors, "transaction date on or after issue date")
		} else {
			score += InvoiceScoreTxnBeforeIssuePenalty
			factors = append(factors, "transaction date before issue date penalty")
		}
	}

	if referenceContainsInvoiceNumber(txn.Reference, candidate.Number) {
		score += InvoiceScoreReferenceContainsBonus
		if candidate.Amount.Amount == txn.Amount.Amount && score < InvoiceScoreReferenceNearConclusive {
			score = InvoiceScoreReferenceNearConclusive
		}
		factors = append(factors, "reference contains invoice number")
	}

	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	return roundConfidence(score), factors, true
}

func invoiceMatchExplanation(match invoiceMatchResult) string {
	number := strings.TrimSpace(match.candidate.Number)
	if number == "" {
		number = "draft invoice " + match.candidate.InvoiceID
	}
	if strings.TrimSpace(match.candidate.Status) == "draft" {
		return fmt.Sprintf("%d%% draft invoice match for %s: confirming will send the invoice before allocating payment; %s", confidencePercent(match.score), number, strings.Join(match.factors, ", "))
	}
	return fmt.Sprintf("%d%% invoice match for %s: %s", confidencePercent(match.score), number, strings.Join(match.factors, ", "))
}

func referenceContainsInvoiceNumber(reference string, number string) bool {
	return normalizedContainsTokenSequence(reference, number)
}

func normalizedContainsTokenSequence(value string, sequence string) bool {
	normalizedValue := NormalizePayee(value)
	normalizedSequence := NormalizePayee(sequence)
	if normalizedValue == "" || normalizedSequence == "" {
		return false
	}
	valueTokens := strings.Fields(normalizedValue)
	sequenceTokens := strings.Fields(normalizedSequence)
	if len(sequenceTokens) == 0 || len(sequenceTokens) > len(valueTokens) {
		return false
	}
	for start := 0; start <= len(valueTokens)-len(sequenceTokens); start++ {
		matched := true
		for offset, token := range sequenceTokens {
			if valueTokens[start+offset] != token {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func (s *Service) payeeRuleDecision(ctx context.Context, tx db.Tx, txn Transaction) (*suggestionDecision, error) {
	rules, err := s.store.MatchingPayeeRules(ctx, tx, txn.Payee)
	if err != nil {
		return nil, err
	}
	if len(rules) == 0 {
		return nil, nil
	}
	rule := rules[0]
	return &suggestionDecision{
		input:     payeeRuleSuggestionInput(txn.ID, rule, s.payeeRuleAutoPostThreshold),
		payeeRule: &rule,
	}, nil
}

func payeeRuleSuggestionInput(txnID TransactionID, rule PayeeRule, autoPostThreshold int) SuggestionInput {
	autoPostable := autoPostThreshold > 0 && rule.TimesApplied >= autoPostThreshold
	appliedWord := "times"
	if rule.TimesApplied == 1 {
		appliedWord = "time"
	}
	return SuggestionInput{
		TransactionID: txnID,
		Kind:          SuggestionKindPayeeRule,
		Confidence:    PayeeRuleSuggestionConfidence,
		Target:        string(rule.AccountCode),
		Explanation:   fmt.Sprintf("Payee rule matched %q; applied %d %s", rule.Matcher, rule.TimesApplied, appliedWord),
		AutoPostable:  autoPostable,
	}
}

func (s *Service) dlaDecision(ctx context.Context, txn Transaction) (*suggestionDecision, error) {
	return s.dlaDecisionWithDirectorNames(ctx, txn, nil)
}

func (s *Service) dlaDecisionWithDirectorNames(ctx context.Context, txn Transaction, directorNames DirectorNameSource) (*suggestionDecision, error) {
	if txn.Amount.Amount >= 0 {
		return nil, nil
	}
	normalizedPayee := NormalizePayee(txn.Payee)
	if normalizedPayee == "" {
		return nil, nil
	}

	if directorNames == nil {
		directorNames = s.directorNames
	}
	if directorNames != nil {
		names, err := directorNames.DirectorNames(ctx)
		if err != nil {
			return nil, fmt.Errorf("banking: director names: %w", err)
		}
		for _, name := range names {
			if normalizedSimilarity(txn.Payee, name) >= 0.70 {
				return &suggestionDecision{input: SuggestionInput{
					TransactionID: txn.ID,
					Kind:          SuggestionKindDLA,
					Confidence:    DLADirectorNameConfidence,
					Target:        dlaSuggestionTarget,
					Explanation:   fmt.Sprintf("DLA suggestion: payee resembles director %q; File to DLA with Recode alternative", strings.TrimSpace(name)),
				}}, nil
			}
		}
	}

	for _, pattern := range s.dlaPersonalPatterns {
		normalizedPattern := NormalizePayee(pattern)
		if normalizedPattern == "" {
			continue
		}
		if normalizedContainsTokenSequence(normalizedPayee, normalizedPattern) {
			return &suggestionDecision{input: SuggestionInput{
				TransactionID: txn.ID,
				Kind:          SuggestionKindDLA,
				Confidence:    DLAPersonalPatternConfidence,
				Target:        dlaSuggestionTarget,
				Explanation:   fmt.Sprintf("DLA suggestion: payee matches personal pattern %q; File to DLA with Recode alternative", normalizedPattern),
			}}, nil
		}
	}
	return nil, nil
}

func defaultDLAPersonalPatterns() []string {
	return []string{
		"personal",
		"director drawing",
		"directors drawing",
		"owner drawing",
		"director loan",
	}
}

func normalizedSimilarity(a string, b string) float64 {
	left := NormalizePayee(a)
	right := NormalizePayee(b)
	if left == "" || right == "" {
		return 0
	}
	if left == right {
		return 1
	}
	if strings.Contains(left, right) || strings.Contains(right, left) {
		return 0.95
	}

	tokenScore := tokenOverlap(left, right)
	editScore := editSimilarity(left, right)
	if editScore > tokenScore {
		return editScore
	}
	return tokenScore
}

func tokenOverlap(a string, b string) float64 {
	left := uniqueTokenSet(strings.Fields(a))
	right := uniqueTokenSet(strings.Fields(b))
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	common := 0
	for token := range left {
		if right[token] {
			common++
		}
	}
	denominator := max(len(left), len(right))
	return float64(common) / float64(denominator)
}

func uniqueTokenSet(tokens []string) map[string]bool {
	set := make(map[string]bool, len(tokens))
	for _, token := range tokens {
		set[token] = true
	}
	return set
}

func editSimilarity(a string, b string) float64 {
	distance := levenshteinDistance(a, b)
	longest := max(len([]rune(a)), len([]rune(b)))
	if longest == 0 {
		return 0
	}
	score := 1 - float64(distance)/float64(longest)
	if score < 0 {
		return 0
	}
	return score
}

func levenshteinDistance(a string, b string) int {
	left := []rune(a)
	right := []rune(b)
	if len(left) == 0 {
		return len(right)
	}
	if len(right) == 0 {
		return len(left)
	}

	previous := make([]int, len(right)+1)
	current := make([]int, len(right)+1)
	for j := range previous {
		previous[j] = j
	}
	for i, lr := range left {
		current[0] = i + 1
		for j, rr := range right {
			cost := 0
			if lr != rr {
				cost = 1
			}
			current[j+1] = min(
				current[j]+1,
				previous[j+1]+1,
				previous[j]+cost,
			)
		}
		previous, current = current, previous
	}
	return previous[len(right)]
}

func scoreDateOnly(t time.Time) time.Time {
	year, month, day := t.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func roundConfidence(value float64) float64 {
	return math.Round(value*1000) / 1000
}

func confidencePercent(value float64) int {
	return int(math.Round(value * 100))
}
