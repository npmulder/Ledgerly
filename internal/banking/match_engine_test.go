package banking

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

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
	if referenceContainsInvoiceNumber("Payment INV-10", "INV-1") {
		t.Fatal("referenceContainsInvoiceNumber() matched INV-1 inside INV-10")
	}
	if !referenceContainsInvoiceNumber("Payment INV-1", "INV-1") {
		t.Fatal("referenceContainsInvoiceNumber() did not match exact invoice token sequence")
	}
	if score := tokenOverlap("john john", "john smith"); score != 0.5 {
		t.Fatalf("tokenOverlap() duplicate partial score = %.3f, want 0.500", score)
	}
	if score := normalizedSimilarity("John John", "John Smith"); score >= 0.70 {
		t.Fatalf("normalizedSimilarity() duplicate partial score = %.3f, want below match threshold", score)
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

	amountMismatchWithRef := base
	amountMismatchWithRef.InvoiceID = "amount-mismatch-with-ref"
	amountMismatchWithRef.Amount = money.Money{Amount: 90000, Currency: "GBP"}
	gotAmountMismatchWithRef, ok := bestInvoiceMatch(txn, []InvoiceMatchCandidate{amountMismatchWithRef})
	if !ok {
		t.Fatal("bestInvoiceMatch() amount mismatch with ref = false, want scored candidate")
	}
	if gotAmountMismatchWithRef.score >= InvoiceHighConfidenceThreshold {
		t.Fatalf("amount mismatch with ref score = %.3f, want below high-confidence threshold", gotAmountMismatchWithRef.score)
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

	paid := base
	paid.InvoiceID = "paid"
	paid.Status = "paid"
	if _, ok := bestInvoiceMatch(txn, []InvoiceMatchCandidate{paid}); ok {
		t.Fatal("bestInvoiceMatch() paid = true, want excluded")
	}

	draft := base
	draft.InvoiceID = "draft"
	draft.Number = ""
	draft.Status = "draft"
	draftMatch, ok := bestInvoiceMatch(txn, []InvoiceMatchCandidate{draft})
	if !ok {
		t.Fatal("bestInvoiceMatch() draft = false, want draft candidate")
	}
	draftExplanation := invoiceMatchExplanation(draftMatch)
	for _, want := range []string{"draft invoice match", "will send the invoice before allocating payment"} {
		if !strings.Contains(draftExplanation, want) {
			t.Fatalf("draft explanation %q missing %q", draftExplanation, want)
		}
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

func TestDLAPersonalPatternsUseTokenBoundaries(t *testing.T) {
	service := NewService(nil, nil, WithDLAPersonalPatterns([]string{"personal"}))
	for _, payee := range []string{"Personalised Gifts Ltd", "Impersonal Software"} {
		decision, err := service.dlaDecision(context.Background(), Transaction{
			ID:     42,
			Amount: money.Money{Amount: -50000, Currency: "GBP"},
			Payee:  payee,
		})
		if err != nil {
			t.Fatalf("dlaDecision(%q) error = %v", payee, err)
		}
		if decision != nil {
			t.Fatalf("dlaDecision(%q) = %#v, want no substring false positive", payee, decision.input)
		}
	}

	decision, err := service.dlaDecision(context.Background(), Transaction{
		ID:        43,
		Amount:    money.Money{Amount: -50000, Currency: "GBP"},
		Payee:     "Director personal drawing",
		Reference: "personal",
	})
	if err != nil {
		t.Fatalf("dlaDecision(personal drawing) error = %v", err)
	}
	if decision == nil || decision.input.Kind != SuggestionKindDLA {
		t.Fatalf("dlaDecision(personal drawing) = %#v, want DLA suggestion", decision)
	}
}

func TestDirectorNameRefreshCreatesUpdatesAndIsIdempotent(t *testing.T) {
	pool, ledgerPool := temporaryMigratedBankingDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	service := NewService(pool, ledger.New(ledgerPool))
	account, err := service.CreateAccount(ctx, AccountInput{
		Name:     "Revolut GBP",
		Provider: ProviderRevolut,
		Currency: "GBP",
	})
	if err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	txnID := importSingleBankingTxn(t, ctx, pool, service, account.ID, revolutTestTxn{
		Date:      time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC),
		ID:        "director-refresh",
		Payee:     "Jane Roberts",
		Reference: "shareholder transfer",
		Amount:    money.Money{Amount: -15_000, Currency: "GBP"},
	})
	if suggestions := mustSuggestions(t, ctx, service, txnID); len(suggestions) != 0 {
		t.Fatalf("suggestions before director names = %#v, want none", suggestions)
	}

	run, err := service.RefreshDirectorNameSuggestions(ctx, []string{"Jane Roberts"})
	if err != nil {
		t.Fatalf("RefreshDirectorNameSuggestions() error = %v", err)
	}
	if run.Trigger != MatchEngineTriggerIdentityProfile || !slices.Equal(run.TxnsEvaluated, []TransactionID{txnID}) || len(run.Suggestions) != 1 {
		t.Fatalf("director refresh run = %#v, want identity profile trigger evaluating and suggesting txn", run)
	}
	active := activeSuggestion(t, mustSuggestions(t, ctx, service, txnID))
	if active.Kind != SuggestionKindDLA || active.Target != dlaSuggestionTarget || !strings.Contains(active.Explanation, "Jane Roberts") {
		t.Fatalf("active suggestion = %#v, want Jane Roberts DLA suggestion", active)
	}
	assertStoredTransactionState(t, ctx, pool, txnID, TransactionStateSuggested)

	repeated, err := service.RefreshDirectorNameSuggestions(ctx, []string{" Jane Roberts "})
	if err != nil {
		t.Fatalf("RefreshDirectorNameSuggestions() repeated error = %v", err)
	}
	if len(repeated.Suggestions) != 0 {
		t.Fatalf("repeated director refresh suggestions = %#v, want no duplicate rows", repeated.Suggestions)
	}
	history := mustSuggestions(t, ctx, service, txnID)
	if len(history) != 1 {
		t.Fatalf("suggestion history after repeated refresh = %#v, want one row", history)
	}
	if got := activeSuggestion(t, history); got.ID != active.ID {
		t.Fatalf("active suggestion after repeated refresh = %d, want existing %d", got.ID, active.ID)
	}

	cleared, err := service.RefreshDirectorNameSuggestions(ctx, []string{"Different Director"})
	if err != nil {
		t.Fatalf("RefreshDirectorNameSuggestions() renamed error = %v", err)
	}
	if len(cleared.Suggestions) != 0 {
		t.Fatalf("renamed director refresh suggestions = %#v, want no replacement suggestion", cleared.Suggestions)
	}
	for _, suggestion := range mustSuggestions(t, ctx, service, txnID) {
		if suggestion.SupersededAt == nil {
			t.Fatalf("active suggestion after director rename = %#v, want cleared", suggestion)
		}
	}
	assertStoredTransactionState(t, ctx, pool, txnID, TransactionStateUnreconciled)

	excludedTxnID := importSingleBankingTxn(t, ctx, pool, service, account.ID, revolutTestTxn{
		Date:      time.Date(2026, 7, 8, 9, 0, 0, 0, time.UTC),
		ID:        "director-refresh-excluded",
		Payee:     "Excluded Person",
		Reference: "excluded transfer",
		Amount:    money.Money{Amount: -8_000, Currency: "GBP"},
	})
	mustTransition(t, ctx, service, excludedTxnID, TransactionStateExcluded, "reviewer")
	if _, err := service.RefreshDirectorNameSuggestions(ctx, []string{"Excluded Person"}); err != nil {
		t.Fatalf("RefreshDirectorNameSuggestions() excluded error = %v", err)
	}
	if suggestions := mustSuggestions(t, ctx, service, excludedTxnID); len(suggestions) != 0 {
		t.Fatalf("excluded transaction suggestions = %#v, want none", suggestions)
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

func TestPayeeRuleAutoPostThresholdZeroDisablesAutoPost(t *testing.T) {
	service := NewService(nil, nil, WithPayeeRuleAutoPostThreshold(0))
	input := payeeRuleSuggestionInput(1, PayeeRule{
		Matcher:      "saas vendor ltd",
		AccountCode:  "6200-software",
		TimesApplied: 99,
	}, service.payeeRuleAutoPostThreshold)
	if input.AutoPostable {
		t.Fatalf("auto_postable = true, want false when threshold is disabled")
	}
}

func TestPayeeRuleApplicationCountSerializedByTransactionLock(t *testing.T) {
	pool, ledgerPool := temporaryMigratedBankingDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	service := NewService(pool, ledger.New(ledgerPool), WithPayeeRuleAutoPostThreshold(1))
	account, err := service.CreateAccount(ctx, AccountInput{
		Name:     "Revolut GBP",
		Provider: ProviderRevolut,
		Currency: "GBP",
	})
	if err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}
	txnID := importSingleBankingTxn(t, ctx, pool, service, account.ID, revolutTestTxn{
		Date:      time.Date(2026, 7, 3, 9, 0, 0, 0, time.UTC),
		ID:        "payee-rule-lock",
		Payee:     "Race Vendor Ltd",
		Reference: "subscription race",
		Amount:    money.Money{Amount: -2400, Currency: "GBP"},
	})
	rule, err := service.CreatePayeeRule(ctx, PayeeRuleInput{
		Matcher:     "Race Vendor Ltd",
		MatchMode:   PayeeRuleMatchExact,
		AccountCode: "5010-software",
		CreatedFrom: PayeeRuleCreatedFromManual,
	})
	if err != nil {
		t.Fatalf("CreatePayeeRule() error = %v", err)
	}

	tx1, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx1: %v", err)
	}
	defer func() {
		_ = tx1.Rollback(ctx)
	}()
	tx2, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx2: %v", err)
	}
	defer func() {
		_ = tx2.Rollback(ctx)
	}()

	lockedTxn, canWrite, err := service.canWriteMatchEngineSuggestion(ctx, tx1, txnID)
	if err != nil {
		t.Fatalf("canWriteMatchEngineSuggestion(tx1) error = %v", err)
	}
	if !canWrite {
		t.Fatal("canWriteMatchEngineSuggestion(tx1) = false, want true")
	}
	if lockedTxn.ID != txnID {
		t.Fatalf("locked transaction ID = %d, want %d", lockedTxn.ID, txnID)
	}
	recorded, err := service.store.PayeeRuleSuggestionRecorded(ctx, tx1, txnID, rule.AccountCode)
	if err != nil {
		t.Fatalf("PayeeRuleSuggestionRecorded(tx1) error = %v", err)
	}
	if recorded {
		t.Fatal("PayeeRuleSuggestionRecorded(tx1) = true, want false before first suggestion")
	}
	applied, err := service.store.RecordPayeeRuleApplied(ctx, tx1, rule.ID)
	if err != nil {
		t.Fatalf("RecordPayeeRuleApplied(tx1) error = %v", err)
	}
	input := payeeRuleSuggestionInput(txnID, applied, service.payeeRuleAutoPostThreshold)
	input.CreatedBy = matchEngineCreatedBy(1)
	if _, err := service.store.InsertSuggestion(ctx, tx1, input); err != nil {
		t.Fatalf("InsertSuggestion(tx1) error = %v", err)
	}

	staleTxn, err := service.store.Transaction(ctx, tx2, txnID)
	if err != nil {
		t.Fatalf("load stale transaction in tx2: %v", err)
	}
	staleDecision, err := service.evaluateTransaction(ctx, tx2, staleTxn)
	if err != nil {
		t.Fatalf("evaluateTransaction(tx2) error = %v", err)
	}
	if staleDecision == nil || staleDecision.payeeRule == nil {
		t.Fatalf("stale decision = %#v, want payee-rule decision", staleDecision)
	}
	if staleDecision.payeeRule.TimesApplied != 0 {
		t.Fatalf("stale decision times_applied = %d, want pre-commit value 0", staleDecision.payeeRule.TimesApplied)
	}

	started := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		close(started)
		_, canWrite, err := service.canWriteMatchEngineSuggestion(ctx, tx2, txnID)
		if err != nil {
			errCh <- err
			return
		}
		if !canWrite {
			errCh <- fmt.Errorf("canWriteMatchEngineSuggestion(tx2) = false, want true for engine-owned active suggestion")
			return
		}
		recorded, err := service.store.PayeeRuleSuggestionRecorded(ctx, tx2, txnID, rule.AccountCode)
		if err != nil {
			errCh <- err
			return
		}
		if !recorded {
			errCh <- fmt.Errorf("PayeeRuleSuggestionRecorded(tx2) = false, want true after tx1 commits")
			return
		}
		currentInput, err := service.payeeRuleSuggestionInputAfterLock(ctx, tx2, txnID, *staleDecision.payeeRule)
		if err != nil {
			errCh <- err
			return
		}
		if !currentInput.AutoPostable {
			errCh <- fmt.Errorf("reloaded payee-rule suggestion auto_postable = false, want true at threshold 1")
			return
		}
		if !strings.Contains(currentInput.Explanation, "applied 1 time") {
			errCh <- fmt.Errorf("reloaded payee-rule explanation = %q, want current applied count", currentInput.Explanation)
			return
		}
		errCh <- nil
	}()
	<-started

	if err := tx1.Commit(ctx); err != nil {
		t.Fatalf("commit tx1: %v", err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("tx2 recorded check error = %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("tx2 recorded check timed out: %v", ctx.Err())
	}
	if err := tx2.Rollback(ctx); err != nil {
		t.Fatalf("rollback tx2: %v", err)
	}
	rules, err := service.MatchingPayeeRules(ctx, "Race Vendor Ltd")
	if err != nil {
		t.Fatalf("MatchingPayeeRules() error = %v", err)
	}
	if len(rules) != 1 || rules[0].TimesApplied != 1 {
		t.Fatalf("matching rules = %#v, want one recorded application", rules)
	}

	excludedTxnID := importSingleBankingTxn(t, ctx, pool, service, account.ID, revolutTestTxn{
		Date:      time.Date(2026, 7, 4, 9, 0, 0, 0, time.UTC),
		ID:        "payee-rule-lock-excluded",
		Payee:     "Race Vendor Ltd",
		Reference: "subscription excluded",
		Amount:    money.Money{Amount: -2400, Currency: "GBP"},
	})
	mustTransition(t, ctx, service, excludedTxnID, TransactionStateExcluded, "reviewer")
	tx3, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx3: %v", err)
	}
	defer func() {
		_ = tx3.Rollback(ctx)
	}()
	lockedTxn, canWrite, err = service.canWriteMatchEngineSuggestion(ctx, tx3, excludedTxnID)
	if err != nil {
		t.Fatalf("canWriteMatchEngineSuggestion(excluded) error = %v", err)
	}
	if canWrite {
		t.Fatal("canWriteMatchEngineSuggestion(excluded) = true, want false")
	}
	if lockedTxn.State != TransactionStateExcluded {
		t.Fatalf("locked transaction state = %q, want excluded", lockedTxn.State)
	}
}

func TestTargetedMatchEngineRunsUseStableTransactionOrder(t *testing.T) {
	pool, ledgerPool := temporaryMigratedBankingDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	service := NewService(pool, ledger.New(ledgerPool))
	account, err := service.CreateAccount(ctx, AccountInput{
		Name:     "Revolut GBP",
		Provider: ProviderRevolut,
		Currency: "GBP",
	})
	if err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}
	firstTxn := importSingleBankingTxn(t, ctx, pool, service, account.ID, revolutTestTxn{
		Date:      time.Date(2026, 7, 5, 9, 0, 0, 0, time.UTC),
		ID:        "stable-order-1",
		Payee:     "Stationery Shop",
		Reference: "office paper",
		Amount:    money.Money{Amount: -1500, Currency: "GBP"},
	})
	secondTxn := importSingleBankingTxn(t, ctx, pool, service, account.ID, revolutTestTxn{
		Date:      time.Date(2026, 7, 6, 9, 0, 0, 0, time.UTC),
		ID:        "stable-order-2",
		Payee:     "Coffee Supplies",
		Reference: "office coffee",
		Amount:    money.Money{Amount: -2100, Currency: "GBP"},
	})

	run, err := service.RunMatchEngine(ctx, MatchEngineTriggerManualRefresh, []TransactionID{secondTxn, firstTxn})
	if err != nil {
		t.Fatalf("RunMatchEngine() error = %v", err)
	}
	want := []TransactionID{firstTxn, secondTxn}
	if !slices.Equal(run.TxnsEvaluated, want) {
		t.Fatalf("txns evaluated = %v, want stable id order %v", run.TxnsEvaluated, want)
	}
	assertLastEngineRun(t, ctx, pool, MatchEngineTriggerManualRefresh, want)
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

func TestDefaultInvoiceCandidatesUseOpenSentAndDraftInvoicingRecords(t *testing.T) {
	pool, ledgerPool := temporaryMigratedBankingDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	invoicingPool := openBankingTestRolePool(t, ctx, bankingTestDatabaseURL(t), pool.Config().ConnConfig.Database, "ledgerly_invoicing", db.WithModule(invoicing.ModuleName))
	t.Cleanup(invoicingPool.Close)
	seedSentInvoiceCandidate(t, ctx, invoicingPool, sentInvoiceSeed{
		InvoiceID:  "default-source-invoice",
		Number:     "INV-2026-31",
		ClientID:   "default-source-client",
		ClientName: "Default Source Ltd",
		IssueDate:  time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		DueDate:    time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
		Amount:     money.Money{Amount: 310000, Currency: "GBP"},
	})
	seedSentInvoiceCandidate(t, ctx, invoicingPool, sentInvoiceSeed{
		InvoiceID:  "default-source-draft",
		ClientID:   "default-source-draft-client",
		ClientName: "Draft Source Ltd",
		IssueDate:  time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
		DueDate:    time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		Amount:     money.Money{Amount: 320000, Currency: "GBP"},
		Status:     "draft",
	})

	service := NewService(pool, ledger.New(ledgerPool))
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
		ID:        "default-source-match",
		Payee:     "Default Source Ltd",
		Reference: "Payment INV-2026-31",
		Amount:    money.Money{Amount: 310000, Currency: "GBP"},
	})

	history, err := service.SuggestionsForTransaction(ctx, txnID)
	if err != nil {
		t.Fatalf("SuggestionsForTransaction() error = %v", err)
	}
	active := activeSuggestion(t, history)
	if active.Kind != SuggestionKindInvoiceMatch || active.Target != "default-source-invoice" {
		t.Fatalf("active suggestion = %#v, want default-source invoice match", active)
	}

	draftTxnID := importSingleBankingTxn(t, ctx, pool, service, account.ID, revolutTestTxn{
		Date:      time.Date(2026, 7, 21, 9, 0, 0, 0, time.UTC),
		ID:        "default-source-draft-match",
		Payee:     "Draft Source Ltd",
		Reference: "bank transfer",
		Amount:    money.Money{Amount: 320000, Currency: "GBP"},
	})

	draftHistory, err := service.SuggestionsForTransaction(ctx, draftTxnID)
	if err != nil {
		t.Fatalf("SuggestionsForTransaction() draft error = %v", err)
	}
	draftActive := activeSuggestion(t, draftHistory)
	if draftActive.Kind != SuggestionKindInvoiceMatch || draftActive.Target != "default-source-draft" {
		t.Fatalf("active draft suggestion = %#v, want default-source draft invoice match", draftActive)
	}
	if !strings.Contains(draftActive.Explanation, "draft invoice match") {
		t.Fatalf("draft suggestion explanation = %q, want draft invoice match", draftActive.Explanation)
	}
}

func TestInvoiceSentSubscriberWritesThroughPublisherTransaction(t *testing.T) {
	pool, ledgerPool := temporaryMigratedBankingDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	candidates := &mutableInvoiceCandidates{}
	service := NewService(pool, ledger.New(ledgerPool), WithInvoiceCandidates(candidates))
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
		ID:        "invoice-sent-shared-tx",
		Payee:     "ACME Consulting",
		Reference: "Payment INV-2026-20",
		Amount:    money.Money{Amount: 200000, Currency: "GBP"},
	})
	candidates.items = []InvoiceMatchCandidate{{
		InvoiceID:  "invoice-20",
		Number:     "INV-2026-20",
		ClientName: "ACME Consulting",
		IssueDate:  time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		DueDate:    time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
		TermsDays:  30,
		Amount:     money.Money{Amount: 200000, Currency: "GBP"},
		Status:     "sent",
	}}

	eventBus := bus.New()
	service.SubscribeEvents(eventBus)
	invoicingPool := openBankingTestRolePool(t, ctx, bankingTestDatabaseURL(t), pool.Config().ConnConfig.Database, "ledgerly_invoicing", db.WithModule(invoicing.ModuleName))
	t.Cleanup(invoicingPool.Close)
	tx, err := invoicingPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin publisher transaction: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.Background())
		}
	}()

	if err := eventBus.Publish(ctx, tx, invoicing.InvoiceSent{InvoiceID: "invoice-20"}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	assertCurrentScope(t, ctx, tx, "ledgerly_invoicing", "invoicing")
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit publisher transaction: %v", err)
	}
	committed = true

	history, err := service.SuggestionsForTransaction(ctx, txnID)
	if err != nil {
		t.Fatalf("SuggestionsForTransaction() error = %v", err)
	}
	active := activeSuggestion(t, history)
	if active.Kind != SuggestionKindInvoiceMatch || active.Target != "invoice-20" {
		t.Fatalf("active suggestion = %#v, want invoice-20 match", active)
	}
	var invoiceSentRuns int
	if err := pool.QueryRow(ctx, `
SELECT count(*)::integer
FROM match_engine_runs
WHERE trigger = 'invoicing.InvoiceSent'`).Scan(&invoiceSentRuns); err != nil {
		t.Fatalf("count InvoiceSent runs: %v", err)
	}
	if invoiceSentRuns != 1 {
		t.Fatalf("InvoiceSent runs after commit = %d, want 1", invoiceSentRuns)
	}
}

func TestInvoiceSentUsesPublisherTransactionRollback(t *testing.T) {
	pool, ledgerPool := temporaryMigratedBankingDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	candidates := &mutableInvoiceCandidates{}
	service := NewService(pool, ledger.New(ledgerPool), WithInvoiceCandidates(candidates))
	account, err := service.CreateAccount(ctx, AccountInput{
		Name:     "Revolut GBP",
		Provider: ProviderRevolut,
		Currency: "GBP",
	})
	if err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}
	txnID := importSingleBankingTxn(t, ctx, pool, service, account.ID, revolutTestTxn{
		Date:      time.Date(2026, 7, 21, 9, 0, 0, 0, time.UTC),
		ID:        "invoice-sent-rollback",
		Payee:     "ACME Consulting",
		Reference: "Payment INV-2026-21",
		Amount:    money.Money{Amount: 210000, Currency: "GBP"},
	})
	candidates.items = []InvoiceMatchCandidate{{
		InvoiceID:  "invoice-21",
		Number:     "INV-2026-21",
		ClientName: "ACME Consulting",
		IssueDate:  time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		DueDate:    time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
		TermsDays:  30,
		Amount:     money.Money{Amount: 210000, Currency: "GBP"},
		Status:     "sent",
	}}

	eventBus := bus.New()
	service.SubscribeEvents(eventBus)
	forced := errors.New("force rollback")
	eventBus.Subscribe(invoicing.InvoiceSentName, func(ctx context.Context, tx db.Tx, _ bus.Event) error {
		assertCurrentScope(t, ctx, tx, "ledgerly_invoicing", "invoicing")
		return forced
	})
	invoicingPool := openBankingTestRolePool(t, ctx, bankingTestDatabaseURL(t), pool.Config().ConnConfig.Database, "ledgerly_invoicing", db.WithModule(invoicing.ModuleName))
	t.Cleanup(invoicingPool.Close)
	tx, err := invoicingPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin publisher transaction: %v", err)
	}
	err = eventBus.Publish(ctx, tx, invoicing.InvoiceSent{InvoiceID: "invoice-21"})
	if !errors.Is(err, forced) {
		t.Fatalf("Publish() error = %v, want forced rollback", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback publisher transaction: %v", err)
	}

	history, err := service.SuggestionsForTransaction(ctx, txnID)
	if err != nil {
		t.Fatalf("SuggestionsForTransaction() error = %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("suggestions after rolled back InvoiceSent = %#v, want none", history)
	}
	var invoiceSentRuns int
	if err := pool.QueryRow(ctx, `
SELECT count(*)::integer
FROM match_engine_runs
WHERE trigger = 'invoicing.InvoiceSent'`).Scan(&invoiceSentRuns); err != nil {
		t.Fatalf("count InvoiceSent runs: %v", err)
	}
	if invoiceSentRuns != 0 {
		t.Fatalf("InvoiceSent runs after rollback = %d, want 0", invoiceSentRuns)
	}
}

func assertCurrentScope(t testing.TB, ctx context.Context, tx db.Tx, wantRole string, wantSearchPath string) {
	t.Helper()

	var role string
	var searchPath string
	if err := tx.QueryRow(ctx, "SELECT current_role, current_setting('search_path')").Scan(&role, &searchPath); err != nil {
		t.Fatalf("read transaction scope: %v", err)
	}
	if role != wantRole || searchPath != wantSearchPath {
		t.Fatalf("transaction scope = role %q path %q, want role %q path %q", role, searchPath, wantRole, wantSearchPath)
	}
}

func TestManualRefreshClearsStaleSuggestionWhenDecisionDisappears(t *testing.T) {
	pool, ledgerPool := temporaryMigratedBankingDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	candidates := &mutableInvoiceCandidates{}
	service := NewService(pool, ledger.New(ledgerPool), WithInvoiceCandidates(candidates))
	account, err := service.CreateAccount(ctx, AccountInput{
		Name:     "Revolut GBP",
		Provider: ProviderRevolut,
		Currency: "GBP",
	})
	if err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}
	txnID := importSingleBankingTxn(t, ctx, pool, service, account.ID, revolutTestTxn{
		Date:      time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC),
		ID:        "stale-suggestion-clear",
		Payee:     "ACME Consulting",
		Reference: "Payment INV-2026-22",
		Amount:    money.Money{Amount: 220000, Currency: "GBP"},
	})
	candidates.items = []InvoiceMatchCandidate{{
		InvoiceID:  "invoice-22",
		Number:     "INV-2026-22",
		ClientName: "ACME Consulting",
		IssueDate:  time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		DueDate:    time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
		TermsDays:  30,
		Amount:     money.Money{Amount: 220000, Currency: "GBP"},
		Status:     "sent",
	}}
	if _, err := service.ManualRefresh(ctx); err != nil {
		t.Fatalf("ManualRefresh() with invoice candidate error = %v", err)
	}
	if active := activeSuggestion(t, mustSuggestions(t, ctx, service, txnID)); active.Kind != SuggestionKindInvoiceMatch {
		t.Fatalf("active suggestion kind = %q, want invoice-match", active.Kind)
	}
	assertStoredTransactionState(t, ctx, pool, txnID, TransactionStateSuggested)

	candidates.items = nil
	if _, err := service.ManualRefresh(ctx); err != nil {
		t.Fatalf("ManualRefresh() without candidate error = %v", err)
	}
	history := mustSuggestions(t, ctx, service, txnID)
	for _, suggestion := range history {
		if suggestion.SupersededAt == nil {
			t.Fatalf("active suggestion after candidate disappeared = %#v, want none", suggestion)
		}
	}
	assertStoredTransactionState(t, ctx, pool, txnID, TransactionStateUnreconciled)

	manualTxn := importSingleBankingTxn(t, ctx, pool, service, account.ID, revolutTestTxn{
		Date:      time.Date(2026, 7, 23, 9, 0, 0, 0, time.UTC),
		ID:        "manual-suggestion-preserve",
		Payee:     "Manual Advisor Payee",
		Reference: "manual suggestion",
		Amount:    money.Money{Amount: 230000, Currency: "GBP"},
	})
	manualSuggestion, err := service.RecordSuggestion(ctx, SuggestionInput{
		TransactionID: manualTxn,
		Kind:          SuggestionKindPayeeRule,
		Confidence:    0.72,
		Target:        "6200-software",
		Explanation:   "advisor-created suggestion",
		CreatedBy:     "advisor",
	})
	if err != nil {
		t.Fatalf("RecordSuggestion() manual error = %v", err)
	}
	if _, err := service.ManualRefresh(ctx); err != nil {
		t.Fatalf("ManualRefresh() with manual suggestion error = %v", err)
	}
	active := activeSuggestion(t, mustSuggestions(t, ctx, service, manualTxn))
	if active.ID != manualSuggestion.ID {
		t.Fatalf("active manual suggestion ID = %d, want preserved %d", active.ID, manualSuggestion.ID)
	}
	assertStoredTransactionState(t, ctx, pool, manualTxn, TransactionStateSuggested)

	manualDecisionTxn := importSingleBankingTxn(t, ctx, pool, service, account.ID, revolutTestTxn{
		Date:      time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC),
		ID:        "manual-suggestion-decision-preserve",
		Payee:     "ACME Consulting",
		Reference: "Payment INV-2026-24",
		Amount:    money.Money{Amount: 240000, Currency: "GBP"},
	})
	manualDecisionSuggestion, err := service.RecordSuggestion(ctx, SuggestionInput{
		TransactionID: manualDecisionTxn,
		Kind:          SuggestionKindPayeeRule,
		Confidence:    0.72,
		Target:        "6200-software",
		Explanation:   "advisor-created suggestion",
		CreatedBy:     "advisor",
	})
	if err != nil {
		t.Fatalf("RecordSuggestion() manual decision error = %v", err)
	}
	candidates.items = []InvoiceMatchCandidate{{
		InvoiceID:  "invoice-24",
		Number:     "INV-2026-24",
		ClientName: "ACME Consulting",
		IssueDate:  time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		DueDate:    time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
		TermsDays:  30,
		Amount:     money.Money{Amount: 240000, Currency: "GBP"},
		Status:     "sent",
	}}
	if _, err := service.ManualRefresh(ctx); err != nil {
		t.Fatalf("ManualRefresh() with manual suggestion and invoice decision error = %v", err)
	}
	active = activeSuggestion(t, mustSuggestions(t, ctx, service, manualDecisionTxn))
	if active.ID != manualDecisionSuggestion.ID {
		t.Fatalf("active manual suggestion ID = %d, want preserved %d despite match-engine decision", active.ID, manualDecisionSuggestion.ID)
	}
	assertStoredTransactionState(t, ctx, pool, manualDecisionTxn, TransactionStateSuggested)
}

type sentInvoiceSeed struct {
	InvoiceID  string
	Number     string
	ClientID   string
	ClientName string
	IssueDate  time.Time
	DueDate    time.Time
	Amount     money.Money
	Status     string
}

func seedSentInvoiceCandidate(t *testing.T, ctx context.Context, pool *pgxpool.Pool, seed sentInvoiceSeed) {
	t.Helper()
	address := `{"line1":"1 Test Street","line2":"","locality":"Douglas","region":"","postal_code":"IM1 1AA","country":"IM"}`
	if _, err := pool.Exec(ctx, `
INSERT INTO clients (
	id,
	name,
	address,
	vat_number,
	default_currency,
	terms_days,
	vat_treatment
) VALUES ($1, $2, $3::jsonb, NULL, $4, 30, 'reverse-charge-eu-b2b')`,
		seed.ClientID,
		seed.ClientName,
		address,
		seed.Amount.Currency,
	); err != nil {
		t.Fatalf("seed invoice client: %v", err)
	}
	status := seed.Status
	if strings.TrimSpace(status) == "" {
		status = "sent"
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO invoices (
	id,
	number,
	client_id,
	status,
	issue_date,
	due_date,
	currency,
	vat_treatment
) VALUES ($1, NULLIF($2, ''), $3, $4, $5, $6, $7, 'reverse-charge-eu-b2b')`,
		seed.InvoiceID,
		seed.Number,
		seed.ClientID,
		status,
		seed.IssueDate,
		seed.DueDate,
		seed.Amount.Currency,
	); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO invoice_lines (
	id,
	invoice_id,
	position,
	description,
	qty,
	unit_price_amount_minor,
	unit_price_currency
) VALUES ($1, $2, 1, 'Consulting', 1, $3, $4)`,
		seed.InvoiceID+"-line-1",
		seed.InvoiceID,
		seed.Amount.Amount,
		seed.Amount.Currency,
	); err != nil {
		t.Fatalf("seed invoice line: %v", err)
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

func mustSuggestions(t *testing.T, ctx context.Context, service *Service, txnID TransactionID) []Suggestion {
	t.Helper()
	suggestions, err := service.SuggestionsForTransaction(ctx, txnID)
	if err != nil {
		t.Fatalf("SuggestionsForTransaction(%d) error = %v", txnID, err)
	}
	return suggestions
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
