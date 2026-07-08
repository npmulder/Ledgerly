//go:build integration

package it_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/banking"
	"github.com/npmulder/ledgerly/internal/dla"
	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/it/fixtures"
	"github.com/npmulder/ledgerly/internal/it/harness"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
)

func TestBankingDLASuggestionsRefreshAfterIdentityProfileCreated(t *testing.T) {
	h := harness.New(t, harness.Options{ClockStart: time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	account := createBankingAccountViaHTTP(t, h, "Profile Refresh GBP", "GBP")
	summary := importCSVViaHTTP(t, h, banking.AccountID(account.ID), "before-profile.csv", fixtures.RevolutCSV(fixtures.RevolutTxn{
		Date:      time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC),
		ID:        "identity-refresh-before-profile",
		Payee:     "N Meyer",
		Reference: "shareholder transfer",
		Amount:    money.Money{Amount: -15_000, Currency: "GBP"},
		Balance:   money.Money{Amount: -15_000, Currency: "GBP"},
	}))
	assertHTTPBatchSummary(t, summary, 1, 1, 0)
	if queue := getReviewQueueViaHTTP(t, h); len(queue.Suggestions) != 0 {
		t.Fatalf("review queue before profile suggestions = %+v, want none", queue.Suggestions)
	}

	company := fixtures.Company(t, h)
	queue := getReviewQueueViaHTTP(t, h)
	if len(queue.Suggestions) != 1 {
		t.Fatalf("review queue after profile suggestions = %d, want 1; queue=%+v", len(queue.Suggestions), queue)
	}
	card := queue.Suggestions[0]
	if card.Target.Type != "dla" || card.Target.ID != "director-1" || !strings.Contains(card.Explanation, "N. Meyer") {
		t.Fatalf("DLA suggestion = %+v, want director-name DLA suggestion for N. Meyer", card)
	}
	txnID := banking.TransactionID(card.Transaction.ID)
	assertSuggestionRows(t, ctx, h, txnID, 1, 1)
	assertSideEffectRows(t, ctx, h, bankingTxnSource(txnID), 0, 0)

	company.With(fixtures.CompanyYearEnd(time.December, 31))
	queue = getReviewQueueViaHTTP(t, h)
	if len(queue.Suggestions) != 1 || queue.Suggestions[0].Transaction.ID != int64(txnID) {
		t.Fatalf("review queue after repeated profile update = %+v, want same single suggestion", queue.Suggestions)
	}
	assertSuggestionRows(t, ctx, h, txnID, 1, 1)
	assertSideEffectRows(t, ctx, h, bankingTxnSource(txnID), 0, 0)

	secondSummary := importCSVViaHTTP(t, h, banking.AccountID(account.ID), "before-profile-update.csv", fixtures.RevolutCSV(fixtures.RevolutTxn{
		Date:      time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC),
		ID:        "identity-refresh-before-profile-update",
		Payee:     "Jane Roberts",
		Reference: "shareholder transfer update",
		Amount:    money.Money{Amount: -8_000, Currency: "GBP"},
		Balance:   money.Money{Amount: -23_000, Currency: "GBP"},
	}))
	assertHTTPBatchSummary(t, secondSummary, 1, 1, 0)
	if queue := getReviewQueueViaHTTP(t, h); len(queue.Suggestions) != 1 {
		t.Fatalf("review queue before director update = %+v, want only existing suggestion", queue.Suggestions)
	}

	company.With(func(profile *identity.CompanyProfile) {
		profile.Directors = append(profile.Directors, identity.Director{Name: "Jane Roberts"})
	})
	queue = getReviewQueueViaHTTP(t, h)
	if len(queue.Suggestions) != 2 {
		t.Fatalf("review queue after director update suggestions = %d, want 2; queue=%+v", len(queue.Suggestions), queue)
	}
	updateTxnID := banking.TransactionID(0)
	for _, suggestion := range queue.Suggestions {
		if suggestion.Transaction.Reference == "shareholder transfer update" {
			updateTxnID = banking.TransactionID(suggestion.Transaction.ID)
			if suggestion.Target.Type != "dla" || suggestion.Target.ID != "director-3" || !strings.Contains(suggestion.Explanation, "Jane Roberts") {
				t.Fatalf("updated director DLA suggestion = %+v, want Jane Roberts director-name DLA suggestion", suggestion)
			}
		}
	}
	if updateTxnID == 0 {
		t.Fatalf("review queue after director update = %+v, want suggestion for update transaction", queue.Suggestions)
	}
	assertSuggestionRows(t, ctx, h, txnID, 1, 1)
	assertSuggestionRows(t, ctx, h, updateTxnID, 1, 1)
	assertSideEffectRows(t, ctx, h, bankingTxnSource(updateTxnID), 0, 0)
}

type bankingAccountHTTPResponse struct {
	ID int64 `json:"id"`
}

func createBankingAccountViaHTTP(t testing.TB, h *harness.Harness, name string, currency string) bankingAccountHTTPResponse {
	t.Helper()
	body, err := json.Marshal(map[string]string{
		"name":     name,
		"provider": string(banking.ProviderRevolut),
		"currency": currency,
	})
	if err != nil {
		t.Fatalf("marshal banking account request: %v", err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "/api/banking/accounts", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create POST /api/banking/accounts request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	response := doHarnessRequest(t, h, req, http.StatusCreated)
	var account bankingAccountHTTPResponse
	decodeJSON(t, response, &account)
	return account
}

func assertSuggestionRows(t testing.TB, ctx context.Context, h *harness.Harness, txnID banking.TransactionID, wantTotal int, wantActive int) {
	t.Helper()
	var total int
	if err := h.BankingPool.QueryRow(ctx, `
SELECT count(*)::integer
FROM suggestions
WHERE txn_id = $1`, int64(txnID)).Scan(&total); err != nil {
		t.Fatalf("count suggestions for %d: %v", txnID, err)
	}
	var active int
	if err := h.BankingPool.QueryRow(ctx, `
SELECT count(*)::integer
FROM suggestions
WHERE txn_id = $1
	AND superseded_at IS NULL`, int64(txnID)).Scan(&active); err != nil {
		t.Fatalf("count active suggestions for %d: %v", txnID, err)
	}
	if total != wantTotal || active != wantActive {
		t.Fatalf("suggestion rows for %d = total %d active %d, want total %d active %d", txnID, total, active, wantTotal, wantActive)
	}
}

func assertSideEffectRows(t testing.TB, ctx context.Context, h *harness.Harness, sourceRef string, wantDLAEntries int, wantLedgerEntries int) {
	t.Helper()
	var dlaEntries int
	if err := h.DB.QueryRow(ctx, `
SELECT count(*)::integer
FROM dla.dla_entries
WHERE source = $1`, sourceRef).Scan(&dlaEntries); err != nil {
		t.Fatalf("count DLA entries for %s: %v", sourceRef, err)
	}
	var ledgerEntries int
	if err := h.DB.QueryRow(ctx, `
SELECT count(*)::integer
FROM ledger.journal_entries
WHERE source_module = $1
	AND source_ref = $2`, dla.ModuleName, sourceRef).Scan(&ledgerEntries); err != nil {
		t.Fatalf("count DLA ledger entries for %s: %v", sourceRef, err)
	}
	if dlaEntries != wantDLAEntries || ledgerEntries != wantLedgerEntries {
		t.Fatalf("side effects for %s = DLA entries %d ledger entries %d, want %d/%d", sourceRef, dlaEntries, ledgerEntries, wantDLAEntries, wantLedgerEntries)
	}
}
