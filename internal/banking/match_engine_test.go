package banking

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/db"
)

func TestInvoiceScorerTable(t *testing.T) {
	txn := Transaction{
		ID:        101,
		Date:      time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC),
		Amount:    money.Money{Amount: 120000, Currency: "GBP"},
		Payee:     "ACME Consulting Ltd",
		Reference: "Payment INV-2026-7 thank you",
	}
	base := InvoiceMatchCandidate{
		InvoiceID:  "invoice-7",
		Number:     "INV-2026-7",
		ClientName: "ACME Consulting",
		IssueDate:  time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		DueDate:    time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
		TermsDays:  30,
		Amount:     money.Money{Amount: 120000, Currency: "GBP"},
		Status:     "sent",
	}

	exact, ok := bestInvoiceMatch(txn, []InvoiceMatchCandidate{base})
	if !ok {
		t.Fatal("bestInvoiceMatch() exact = false, want candidate")
	}
	if exact.score < 0.98 {
		t.Fatalf("exact score = %.3f, want >= 0.98", exact.score)
	}

	amountOnly := base
	amountOnly.InvoiceID = "amount-only"
	amountOnly.Number = "INV-2026-8"
	amountOnly.ClientName = "Other Client"
	amountTxn := txn
	amountTxn.Payee = "Unknown payer"
	amountTxn.Reference = "bank transfer"
	gotAmountOnly, ok := bestInvoiceMatch(amountTxn, []InvoiceMatchCandidate{amountOnly})
	if !ok {
		t.Fatal("bestInvoiceMatch() amount-only = false, want candidate")
	}
	if gotAmountOnly.score < InvoiceSuggestionThreshold || gotAmountOnly.score >= InvoiceHighConfidenceThreshold {
		t.Fatalf("amount-only score = %.3f, want mid band [0.60, 0.95)", gotAmountOnly.score)
	}

	wrongCurrency := base
	wrongCurrency.InvoiceID = "wrong-currency"
	wrongCurrency.Amount = money.Money{Amount: 120000, Currency: "EUR"}
	if _, ok := bestInvoiceMatch(txn, []InvoiceMatchCandidate{wrongCurrency}); ok {
		t.Fatal("bestInvoiceMatch() wrong currency = true, want excluded")
	}

	settled := base
	settled.InvoiceID = "settled"
	settled.Settled = true
	if _, ok := bestInvoiceMatch(txn, []InvoiceMatchCandidate{settled}); ok {
		t.Fatal("bestInvoiceMatch() settled = true, want excluded")
	}

	weaker := base
	weaker.InvoiceID = "weaker"
	weaker.Number = "INV-2026-9"
	weaker.ClientName = "Other Client"
	weaker.Amount = txn.Amount
	stronger := base
	stronger.InvoiceID = "stronger"
	best, ok := bestInvoiceMatch(txn, []InvoiceMatchCandidate{weaker, stronger})
	if !ok {
		t.Fatal("bestInvoiceMatch() two candidates = false, want candidate")
	}
	if best.candidate.InvoiceID != "stronger" {
		t.Fatalf("best invoice ID = %q, want stronger", best.candidate.InvoiceID)
	}
	explanation := invoiceMatchExplanation(best)
	for _, factor := range []string{"exact native amount", "payee resembles client", "reference contains invoice number"} {
		if !strings.Contains(explanation, factor) {
			t.Fatalf("explanation %q missing factor %q", explanation, factor)
		}
	}
}

func TestDLADetectionUsesDirectorNameFixture(t *testing.T) {
	service := NewService(nil, nil, WithDirectorNames(staticDirectorNames{"N. Meyer"}))
	decision, err := service.dlaDecision(context.Background(), Transaction{
		ID:        42,
		Amount:    money.Money{Amount: -50000, Currency: "GBP"},
		Payee:     "Transfer to N Meyer",
		Reference: "personal drawing",
	})
	if err != nil {
		t.Fatalf("dlaDecision() error = %v", err)
	}
	if decision == nil {
		t.Fatal("dlaDecision() = nil, want DLA suggestion")
	}
	if decision.input.Kind != SuggestionKindDLA || decision.input.Target != dlaSuggestionTarget {
		t.Fatalf("DLA decision = %#v, want kind dla and target %q", decision.input, dlaSuggestionTarget)
	}
	if !strings.Contains(decision.input.Explanation, "N. Meyer") {
		t.Fatalf("DLA explanation = %q, want director name", decision.input.Explanation)
	}
}

func TestSuggestionKindPriorityOrderingAllKinds(t *testing.T) {
	decisions := []suggestionDecision{
		{input: SuggestionInput{Kind: SuggestionKindDLA, Confidence: 0.82, Target: "director-loan"}},
		{input: SuggestionInput{Kind: SuggestionKindPayeeRule, Confidence: 0.72, Target: "6200-software"}},
		{input: SuggestionInput{Kind: SuggestionKindInvoiceMatch, Confidence: 0.95, Target: "invoice-1"}},
	}
	slices.SortFunc(decisions, func(a, b suggestionDecision) int {
		return suggestionKindPriority(a.input.Kind) - suggestionKindPriority(b.input.Kind)
	})
	got := []SuggestionKind{decisions[0].input.Kind, decisions[1].input.Kind, decisions[2].input.Kind}
	want := []SuggestionKind{SuggestionKindInvoiceMatch, SuggestionKindPayeeRule, SuggestionKindDLA}
	if !slices.Equal(got, want) {
		t.Fatalf("priority order = %v, want %v", got, want)
	}
}

func TestPayeeRuleLearningAndImportMatchIncrementsTimesApplied(t *testing.T) {
	pool, ledgerPool := temporaryMigratedBankingDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	service := NewService(pool, ledger.New(ledgerPool), WithPayeeRuleAutoPostThreshold(2))
	account, err := service.CreateAccount(ctx, AccountInput{
		Name:     "Revolut GBP",
		Provider: ProviderRevolut,
		Currency: "GBP",
	})
	if err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	firstTxn := importSingleBankingTxn(t, ctx, pool, service, account.ID, revolutTestTxn{
		Date:      time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC),
		ID:        "rule-learn-1",
		Payee:     "SaaS Vendor Ltd",
		Reference: "subscription july",
		Amount:    money.Money{Amount: -2400, Currency: "GBP"},
	})
	rule, err := service.LearnFromRecode(ctx, firstTxn, "6200-software")
	if err != nil {
		t.Fatalf("LearnFromRecode() first error = %v", err)
	}
	if rule.TimesApplied != 1 {
		t.Fatalf("learned rule times_applied = %d, want 1", rule.TimesApplied)
	}

	secondTxn := importSingleBankingTxn(t, ctx, pool, service, account.ID, revolutTestTxn{
		Date:      time.Date(2026, 7, 2, 9, 0, 0, 0, time.UTC),
		ID:        "rule-learn-2",
		Payee:     "SaaS Vendor Ltd",
		Reference: "subscription august",
		Amount:    money.Money{Amount: -2400, Currency: "GBP"},
	})
	history, err := service.SuggestionsForTransaction(ctx, secondTxn)
	if err != nil {
		t.Fatalf("SuggestionsForTransaction() error = %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("suggestions length = %d, want 1", len(history))
	}
	suggestion := history[0]
	if suggestion.Kind != SuggestionKindPayeeRule || suggestion.Target != "6200-software" {
		t.Fatalf("suggestion = %#v, want payee-rule to account", suggestion)
	}
	if !suggestion.AutoPostable {
		t.Fatalf("suggestion auto_postable = false, want true at threshold 2")
	}
	if !strings.Contains(suggestion.Explanation, "applied 2 times") {
		t.Fatalf("suggestion explanation = %q, want applied 2 times", suggestion.Explanation)
	}

	matches, err := service.MatchingPayeeRules(ctx, "SaaS Vendor Ltd")
	if err != nil {
		t.Fatalf("MatchingPayeeRules() error = %v", err)
	}
	if len(matches) != 1 || matches[0].TimesApplied != 2 {
		t.Fatalf("matching rules = %#v, want one rule applied twice", matches)
	}
	assertLastEngineRun(t, ctx, pool, MatchEngineTriggerImportCompletion, []TransactionID{secondTxn})
}

func TestMatchEngineInvoiceSentManualRefreshPriorityAndDeterminism(t *testing.T) {
	pool, ledgerPool := temporaryMigratedBankingDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	candidates := &mutableInvoiceCandidates{}
	service := NewService(pool, ledger.New(ledgerPool),
		WithInvoiceCandidates(candidates),
		WithDirectorNames(staticDirectorNames{"ACME Consulting"}),
	)
	account, err := service.CreateAccount(ctx, AccountInput{
		Name:     "Revolut GBP",
		Provider: ProviderRevolut,
		Currency: "GBP",
	})
	if err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	txnID := importSingleBankingTxn(t, ctx, pool, service, account.ID, revolutTestTxn{
		Date:      time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC),
		ID:        "invoice-sent-priority",
		Payee:     "ACME Consulting",
		Reference: "Payment INV-2026-10",
		Amount:    money.Money{Amount: 120000, Currency: "GBP"},
	})
	if suggestions, err := service.SuggestionsForTransaction(ctx, txnID); err != nil {
		t.Fatalf("SuggestionsForTransaction() initial error = %v", err)
	} else if len(suggestions) != 0 {
		t.Fatalf("initial suggestions = %#v, want none before candidate/rule", suggestions)
	}

	if _, err := service.LearnFromRecode(ctx, txnID, "6200-software"); err != nil {
		t.Fatalf("LearnFromRecode() error = %v", err)
	}
	candidates.items = []InvoiceMatchCandidate{{
		InvoiceID:  "invoice-10",
		Number:     "INV-2026-10",
		ClientName: "ACME Consulting",
		IssueDate:  time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		DueDate:    time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
		TermsDays:  30,
		Amount:     money.Money{Amount: 120000, Currency: "GBP"},
		Status:     "sent",
	}}

	eventBus := bus.New()
	service.SubscribeEvents(eventBus)
	if err := eventBus.Publish(ctx, nil, invoicing.InvoiceSent{InvoiceID: "invoice-10"}); err != nil {
		t.Fatalf("publish InvoiceSent error = %v", err)
	}
	assertLastEngineRun(t, ctx, pool, MatchEngineTriggerInvoiceSent, []TransactionID{txnID})
	history, err := service.SuggestionsForTransaction(ctx, txnID)
	if err != nil {
		t.Fatalf("SuggestionsForTransaction() after InvoiceSent error = %v", err)
	}
	active := activeSuggestion(t, history)
	if active.Kind != SuggestionKindInvoiceMatch {
		t.Fatalf("active suggestion kind = %q, want invoice-match priority over payee-rule", active.Kind)
	}
	firstProjection := suggestionProjection(active)

	refreshRun, err := service.ManualRefresh(ctx)
	if err != nil {
		t.Fatalf("ManualRefresh() error = %v", err)
	}
	if refreshRun.Trigger != MatchEngineTriggerManualRefresh || len(refreshRun.TxnsEvaluated) != 1 || refreshRun.TxnsEvaluated[0] != txnID {
		t.Fatalf("ManualRefresh run = %#v, want same txn re-evaluated", refreshRun)
	}
	refreshedHistory, err := service.SuggestionsForTransaction(ctx, txnID)
	if err != nil {
		t.Fatalf("SuggestionsForTransaction() after refresh error = %v", err)
	}
	secondProjection := suggestionProjection(activeSuggestion(t, refreshedHistory))
	if firstProjection != secondProjection {
		t.Fatalf("determinism projection = %#v then %#v, want identical", firstProjection, secondProjection)
	}
}

type staticDirectorNames []string

func (s staticDirectorNames) DirectorNames(context.Context) ([]string, error) {
	return append([]string{}, s...), nil
}

type mutableInvoiceCandidates struct {
	items []InvoiceMatchCandidate
}

func (m *mutableInvoiceCandidates) InvoiceCandidates(context.Context, db.Tx, string) ([]InvoiceMatchCandidate, error) {
	return append([]InvoiceMatchCandidate{}, m.items...), nil
}

type suggestionProjectionValue struct {
	Kind         SuggestionKind
	Confidence   float64
	Target       string
	Explanation  string
	AutoPostable bool
}

func suggestionProjection(suggestion Suggestion) suggestionProjectionValue {
	return suggestionProjectionValue{
		Kind:         suggestion.Kind,
		Confidence:   suggestion.Confidence,
		Target:       suggestion.Target,
		Explanation:  suggestion.Explanation,
		AutoPostable: suggestion.AutoPostable,
	}
}

func activeSuggestion(t *testing.T, suggestions []Suggestion) Suggestion {
	t.Helper()
	var active []Suggestion
	for _, suggestion := range suggestions {
		if suggestion.SupersededAt == nil {
			active = append(active, suggestion)
		}
	}
	if len(active) != 1 {
		t.Fatalf("active suggestions = %#v, want one active", active)
	}
	return active[0]
}

func assertLastEngineRun(t *testing.T, ctx context.Context, pool queryRower, trigger MatchEngineTrigger, ids []TransactionID) {
	t.Helper()
	var (
		gotTrigger string
		gotIDs     string
	)
	if err := pool.QueryRow(ctx, `
SELECT trigger, txns_evaluated::text
FROM match_engine_runs
ORDER BY id DESC
LIMIT 1`).Scan(&gotTrigger, &gotIDs); err != nil {
		t.Fatalf("load last match engine run: %v", err)
	}
	if MatchEngineTrigger(gotTrigger) != trigger {
		t.Fatalf("last trigger = %q, want %q", gotTrigger, trigger)
	}
	wantIDs := "{"
	for i, id := range ids {
		if i > 0 {
			wantIDs += ","
		}
		wantIDs += strconv.FormatInt(int64(id), 10)
	}
	wantIDs += "}"
	if gotIDs != wantIDs {
		t.Fatalf("last txns_evaluated = %q, want %q", gotIDs, wantIDs)
	}
}
