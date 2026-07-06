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
		Date:      time.Date(2026, 7, 3, 9, 0, 0, 0, time.UTC),
		ID:        "payee-rule-lock",
		Payee:     "Race Vendor Ltd",
		Reference: "subscription race",
		Amount:    money.Money{Amount: -2400, Currency: "GBP"},
	})
	rule, err := service.CreatePayeeRule(ctx, PayeeRuleInput{
		Matcher:     "Race Vendor Ltd",
		MatchMode:   PayeeRuleMatchExact,
		AccountCode: "6200-software",
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

	canWrite, err := service.canWriteMatchEngineSuggestion(ctx, tx1, txnID)
	if err != nil {
		t.Fatalf("canWriteMatchEngineSuggestion(tx1) error = %v", err)
	}
	if !canWrite {
		t.Fatal("canWriteMatchEngineSuggestion(tx1) = false, want true")
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

	started := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		close(started)
		canWrite, err := service.canWriteMatchEngineSuggestion(ctx, tx2, txnID)
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
		var role string
		var searchPath string
		if err := tx.QueryRow(ctx, "SELECT current_role, current_setting('search_path')").Scan(&role, &searchPath); err != nil {
			return err
		}
		if role != "ledgerly_invoicing" || searchPath != "invoicing" {
			return fmt.Errorf("subscriber transaction scope = role %q path %q, want invoicing scope", role, searchPath)
		}
		return forced
	})
	invoicingPool := openBankingTestPool(t, ctx, bankingTestDatabaseURL(t), pool.Config().ConnConfig.Database, db.WithModule(invoicing.ModuleName))
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
