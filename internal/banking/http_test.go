package banking

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/dla"
	"github.com/npmulder/ledgerly/internal/invoicing"
	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	"github.com/npmulder/ledgerly/internal/platform/db"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

func TestHTTPImportRoundTripSummaryOversizeAndAuth(t *testing.T) {
	pool, _ := temporaryMigratedBankingDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	service := NewService(pool, &recordingBankingLedger{})
	router := newBankingHTTPTestRouter(t, service)

	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/banking/accounts"},
		{http.MethodPost, "/api/banking/accounts"},
		{http.MethodPost, "/api/banking/accounts/1/import"},
		{http.MethodGet, "/api/banking/review"},
		{http.MethodGet, "/api/banking/feed"},
		{http.MethodGet, "/api/banking/recent"},
		{http.MethodGet, "/api/banking/payee-rules"},
		{http.MethodPost, "/api/banking/payee-rules"},
		{http.MethodPut, "/api/banking/payee-rules/1"},
		{http.MethodDelete, "/api/banking/payee-rules/1"},
		{http.MethodPost, "/api/banking/transactions/1/confirm"},
		{http.MethodPost, "/api/banking/transactions/1/file-dla"},
		{http.MethodPost, "/api/banking/transactions/1/recode"},
		{http.MethodPost, "/api/banking/transactions/1/exclude"},
		{http.MethodPost, "/api/banking/transactions/1/unexclude"},
		{http.MethodPut, "/api/banking/transactions/1/receipt"},
		{http.MethodGet, "/api/banking/transactions/1/receipt"},
		{http.MethodDelete, "/api/banking/transactions/1/receipt"},
	} {
		response := performBankingRequest(router, tc.method, tc.path, nil, "", false)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s status = %d, want %d; body=%s", tc.method, tc.path, response.Code, http.StatusUnauthorized, response.Body.String())
		}
		if got := response.Header().Get("Content-Type"); got != httpserver.ProblemContentType {
			t.Fatalf("%s %s Content-Type = %q, want %s", tc.method, tc.path, got, httpserver.ProblemContentType)
		}
	}

	account := createBankingHTTPAccount(t, router, "Revolut GBP", "GBP")
	firstCSV := revolutTestCSV(
		revolutTestTxn{
			Date:      time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC),
			ID:        "http-import-1",
			Payee:     "Alpha Ltd",
			Reference: "invoice 1",
			Amount:    money.Money{Amount: 10000, Currency: "GBP"},
		},
		revolutTestTxn{
			Date:      time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC),
			ID:        "http-import-2",
			Payee:     "Beta Ltd",
			Reference: "subscription",
			Amount:    money.Money{Amount: -2500, Currency: "GBP"},
		},
	)
	firstImport := performBankingCSVImport(t, router, account.ID, "statement.csv", firstCSV)
	if firstImport.Total != 2 || firstImport.New != 2 || firstImport.Duplicates != 0 {
		t.Fatalf("first import summary = %+v, want total/new/duplicates 2/2/0", firstImport)
	}
	secondImport := performBankingCSVImport(t, router, account.ID, "statement-again.csv", firstCSV)
	if secondImport.Total != 2 || secondImport.New != 0 || secondImport.Duplicates != 2 {
		t.Fatalf("second import summary = %+v, want total/new/duplicates 2/0/2", secondImport)
	}

	accountsResponse := performBankingRequest(router, http.MethodGet, "/api/banking/accounts", nil, "", true)
	if accountsResponse.Code != http.StatusOK {
		t.Fatalf("accounts status = %d, want %d; body=%s", accountsResponse.Code, http.StatusOK, accountsResponse.Body.String())
	}
	var accounts bankingAccountsResponse
	decodeBankingResponse(t, accountsResponse, &accounts)
	if len(accounts.Accounts) != 1 || accounts.Accounts[0].UnreconciledCount != 2 {
		t.Fatalf("accounts response = %+v, want one account with unreconciled_count 2", accounts)
	}

	badCSV := []byte(`Date started (UTC),Date completed (UTC),ID,Type,Description,Reference,Amount,Fee,Currency,State,Balance
2026-09-03 09:00:00,2026-09-03 09:00:00,http-bad,CARD_PAYMENT,Alpha,bad,not-money,0.00,GBP,COMPLETED,0.00
`)
	badBody, badContentType := multipartBody(t, "bad.csv", badCSV)
	badResponse := performBankingRequest(router, http.MethodPost, fmt.Sprintf("/api/banking/accounts/%d/import", account.ID), badBody, badContentType, true)
	if badResponse.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad import status = %d, want %d; body=%s", badResponse.Code, http.StatusUnprocessableEntity, badResponse.Body.String())
	}
	var badProblem map[string]any
	decodeBankingResponse(t, badResponse, &badProblem)
	rows, ok := badProblem["row_numbers"].([]any)
	if !ok || len(rows) != 1 || rows[0].(float64) != 2 {
		t.Fatalf("bad import row_numbers = %#v, want [2]; body=%s", badProblem["row_numbers"], badResponse.Body.String())
	}

	oversized := bytes.Repeat([]byte("x"), maxImportCSVBytes+1)
	oversizedBody, oversizedContentType := multipartBody(t, "too-large.csv", oversized)
	oversizedResponse := performBankingRequest(router, http.MethodPost, fmt.Sprintf("/api/banking/accounts/%d/import", account.ID), oversizedBody, oversizedContentType, true)
	if oversizedResponse.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized import status = %d, want %d; body=%s", oversizedResponse.Code, http.StatusRequestEntityTooLarge, oversizedResponse.Body.String())
	}

	var stored int
	if err := pool.QueryRow(ctx, `SELECT count(*)::integer FROM transactions WHERE account_id = $1`, account.ID).Scan(&stored); err != nil {
		t.Fatalf("count imported transactions: %v", err)
	}
	if stored != 2 {
		t.Fatalf("stored transaction count = %d, want 2", stored)
	}
}

func TestHTTPReviewQueuePayloadIsCardComplete(t *testing.T) {
	pool, _ := temporaryMigratedBankingDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	candidates := &mutableInvoiceCandidates{}
	service := NewService(pool, &recordingBankingLedger{}, WithInvoiceCandidates(candidates))
	router := newBankingHTTPTestRouter(t, service)
	account := createBankingHTTPAccount(t, router, "Revolut GBP", "GBP")

	matchTxn := importSingleBankingTxn(t, ctx, pool, service, AccountID(account.ID), revolutTestTxn{
		Date:      time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC),
		ID:        "http-review-match",
		Payee:     "Contoso Ltd",
		Reference: "INV-HTTP-1",
		Amount:    money.Money{Amount: 120000, Currency: "GBP"},
	})
	suggestionTxn := importSingleBankingTxn(t, ctx, pool, service, AccountID(account.ID), revolutTestTxn{
		Date:      time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC),
		ID:        "http-review-dla",
		Payee:     "Director personal transfer",
		Reference: "drawing",
		Amount:    money.Money{Amount: -50000, Currency: "GBP"},
	})
	ruleTxn := importSingleBankingTxn(t, ctx, pool, service, AccountID(account.ID), revolutTestTxn{
		Date:      time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC),
		ID:        "http-review-rule",
		Payee:     "SaaS Vendor",
		Reference: "subscription",
		Amount:    money.Money{Amount: -2400, Currency: "GBP"},
	})

	candidates.items = []InvoiceMatchCandidate{{
		InvoiceID:  "invoice-http-1",
		Number:     "INV-HTTP-1",
		ClientName: "Contoso Ltd",
		IssueDate:  time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		DueDate:    time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		Amount:     money.Money{Amount: 120000, Currency: "GBP"},
		Status:     "sent",
	}}
	mustRecordSuggestion(t, ctx, service, matchTxn, SuggestionKindInvoiceMatch, 0.982, "invoice-http-1", "98% match - amount + payee + date")
	mustRecordSuggestion(t, ctx, service, suggestionTxn, SuggestionKindDLA, 0.750, "director-loan", "75% suggestion - director payee")
	rule, err := service.CreatePayeeRule(ctx, PayeeRuleInput{
		Matcher:     "SaaS Vendor",
		MatchMode:   PayeeRuleMatchExact,
		AccountCode: "6200-software",
		CreatedFrom: PayeeRuleCreatedFromManual,
	})
	if err != nil {
		t.Fatalf("CreatePayeeRule() error = %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := service.RecordPayeeRuleApplied(ctx, rule.ID); err != nil {
			t.Fatalf("RecordPayeeRuleApplied(%d) error = %v", i, err)
		}
	}
	mustRecordSuggestion(t, ctx, service, ruleTxn, SuggestionKindPayeeRule, 0.910, "6200-software", "91% rule - applied 2 times")

	response := performBankingRequest(router, http.MethodGet, "/api/banking/review", nil, "", true)
	if response.Code != http.StatusOK {
		t.Fatalf("review status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	var queue reviewQueueResponse
	decodeBankingResponse(t, response, &queue)
	if len(queue.Matches) != 1 || len(queue.Suggestions) != 1 || len(queue.Rules) != 1 {
		t.Fatalf("review groups = matches %d suggestions %d rules %d, want 1/1/1", len(queue.Matches), len(queue.Suggestions), len(queue.Rules))
	}
	if card := queue.Matches[0]; card.Kind != "match" || card.Target.InvoiceNumber != "INV-HTTP-1" || card.Target.Client != "Contoso Ltd" || card.Transaction.ID != int64(matchTxn) || card.Confidence == 0 || card.Explanation == "" {
		t.Fatalf("match card = %+v, want complete invoice target/details", card)
	}
	if card := queue.Suggestions[0]; card.Kind != "suggestion" || card.Target.Type != "dla" || card.Target.ID != "director-loan" || card.Transaction.ID != int64(suggestionTxn) || card.Explanation == "" {
		t.Fatalf("suggestion card = %+v, want complete DLA target/details", card)
	}
	if card := queue.Rules[0]; card.Kind != "rule" || card.Target.AccountCode != "6200-software" || card.Target.TimesApplied == nil || *card.Target.TimesApplied != 2 || card.Transaction.ID != int64(ruleTxn) {
		t.Fatalf("rule card = %+v, want account target with times_applied 2", card)
	}
}

func TestHTTPPayeeRuleManagementCRUDUpdatesFutureMatches(t *testing.T) {
	pool, _ := temporaryMigratedBankingDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	service := NewService(pool, &recordingBankingLedger{})
	router := newBankingHTTPTestRouter(t, service)

	createBody := strings.NewReader(`{"matcher":"ACME SaaS Ltd","match_mode":"exact","account_code":"5000-fees"}`)
	createResponse := performBankingRequest(router, http.MethodPost, "/api/banking/payee-rules", createBody, "application/json", true)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create payee rule status = %d, want %d; body=%s", createResponse.Code, http.StatusCreated, createResponse.Body.String())
	}
	var created payeeRuleResponse
	decodeBankingResponse(t, createResponse, &created)
	if created.Matcher != "acme saas ltd" || created.MatchMode != "exact" || created.AccountCode != "5000-fees" || created.CreatedFrom != "manual" || created.TimesApplied != 0 {
		t.Fatalf("created payee rule = %+v, want normalized manual rule", created)
	}

	if _, err := service.RecordPayeeRuleApplied(ctx, PayeeRuleID(created.ID)); err != nil {
		t.Fatalf("RecordPayeeRuleApplied() error = %v", err)
	}
	listResponse := performBankingRequest(router, http.MethodGet, "/api/banking/payee-rules", nil, "", true)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list payee rules status = %d, want %d; body=%s", listResponse.Code, http.StatusOK, listResponse.Body.String())
	}
	var list payeeRulesResponse
	decodeBankingResponse(t, listResponse, &list)
	if len(list.Rules) != 1 || list.Rules[0].ID != created.ID || list.Rules[0].TimesApplied != 1 {
		t.Fatalf("list payee rules = %+v, want one applied rule", list)
	}

	updateBody := strings.NewReader(`{"matcher":"ACME SaaS Ltd","match_mode":"exact","account_code":"5010-software"}`)
	updateResponse := performBankingRequest(router, http.MethodPut, fmt.Sprintf("/api/banking/payee-rules/%d", created.ID), updateBody, "application/json", true)
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("update payee rule status = %d, want %d; body=%s", updateResponse.Code, http.StatusOK, updateResponse.Body.String())
	}
	var updated payeeRuleResponse
	decodeBankingResponse(t, updateResponse, &updated)
	if updated.AccountCode != "5010-software" || updated.TimesApplied != 1 || updated.CreatedFrom != "manual" {
		t.Fatalf("updated payee rule = %+v, want corrected account preserving metadata", updated)
	}
	matches, err := service.MatchingPayeeRules(ctx, "ACME SaaS Ltd")
	if err != nil {
		t.Fatalf("MatchingPayeeRules() error = %v", err)
	}
	if len(matches) != 1 || matches[0].AccountCode != "5010-software" {
		t.Fatalf("matches after update = %+v, want corrected software rule only", matches)
	}

	deleteResponse := performBankingRequest(router, http.MethodDelete, fmt.Sprintf("/api/banking/payee-rules/%d", created.ID), nil, "", true)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("delete payee rule status = %d, want %d; body=%s", deleteResponse.Code, http.StatusNoContent, deleteResponse.Body.String())
	}
	matches, err = service.MatchingPayeeRules(ctx, "ACME SaaS Ltd")
	if err != nil {
		t.Fatalf("MatchingPayeeRules() after delete error = %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("matches after delete = %+v, want none", matches)
	}

	notFoundResponse := performBankingRequest(router, http.MethodDelete, fmt.Sprintf("/api/banking/payee-rules/%d", created.ID), nil, "", true)
	if notFoundResponse.Code != http.StatusNotFound {
		t.Fatalf("delete missing payee rule status = %d, want %d; body=%s", notFoundResponse.Code, http.StatusNotFound, notFoundResponse.Body.String())
	}
}

func TestHTTPFeedRecentAndCommandsHappyAndConflict(t *testing.T) {
	pool, _ := temporaryMigratedBankingDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	ledger := &recordingBankingLedger{}
	service := NewService(
		pool,
		ledger,
		WithLedgerJournal(ledger),
		WithMoneyFX(stubBankingMoneyFX{realised: money.Money{Amount: 321, Currency: "GBP"}}),
		WithInvoicingSettler(stubInvoiceSettler{}),
		WithDLAFileDrawer(&recordingDLAFileDrawer{}),
	)
	router := newBankingHTTPTestRouter(t, service)
	account := createBankingHTTPAccount(t, router, "Revolut GBP", "GBP")

	confirmTxn := importSingleBankingTxn(t, ctx, pool, service, AccountID(account.ID), revolutTestTxn{
		Date:      time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC),
		ID:        "http-command-confirm",
		Payee:     "Client Ltd",
		Reference: "INV-CMD-1",
		Amount:    money.Money{Amount: 50000, Currency: "GBP"},
	})
	dlaTxn := importSingleBankingTxn(t, ctx, pool, service, AccountID(account.ID), revolutTestTxn{
		Date:      time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC),
		ID:        "http-command-dla",
		Payee:     "Director drawing",
		Reference: "drawing",
		Amount:    money.Money{Amount: -12000, Currency: "GBP"},
	})
	recodeTxn := importSingleBankingTxn(t, ctx, pool, service, AccountID(account.ID), revolutTestTxn{
		Date:      time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC),
		ID:        "http-command-recode",
		Payee:     "Software Vendor",
		Reference: "subscription",
		Amount:    money.Money{Amount: -2400, Currency: "GBP"},
	})
	excludeTxn := importSingleBankingTxn(t, ctx, pool, service, AccountID(account.ID), revolutTestTxn{
		Date:      time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC),
		ID:        "http-command-exclude",
		Payee:     "Duplicate",
		Reference: "duplicate",
		Amount:    money.Money{Amount: -1, Currency: "GBP"},
	})
	mustRecordSuggestion(t, ctx, service, confirmTxn, SuggestionKindInvoiceMatch, 0.980, "invoice-command-1", "invoice match")
	mustRecordSuggestion(t, ctx, service, dlaTxn, SuggestionKindDLA, 0.800, "director-loan", "director drawing")
	mustRecordSuggestion(t, ctx, service, recodeTxn, SuggestionKindPayeeRule, 0.900, "6200-software", "payee rule")

	confirmResponse := performBankingRequest(router, http.MethodPost, fmt.Sprintf("/api/banking/transactions/%d/confirm", confirmTxn), nil, "", true)
	if confirmResponse.Code != http.StatusOK {
		t.Fatalf("confirm status = %d, want %d; body=%s", confirmResponse.Code, http.StatusOK, confirmResponse.Body.String())
	}
	var confirm commandResponse
	decodeBankingResponse(t, confirmResponse, &confirm)
	if confirm.Transaction == nil || confirm.Transaction.ID != int64(confirmTxn) || confirm.RealisedFXAmount == nil || confirm.RealisedFXAmount.AmountMinor != 321 {
		t.Fatalf("confirm response = %+v, want transaction and realised_fx_amount 321", confirm)
	}
	assertBankingConflict(t, router, http.MethodPost, fmt.Sprintf("/api/banking/transactions/%d/confirm", confirmTxn), nil)

	fileResponse := performBankingRequest(router, http.MethodPost, fmt.Sprintf("/api/banking/transactions/%d/file-dla", dlaTxn), nil, "", true)
	if fileResponse.Code != http.StatusOK {
		t.Fatalf("file-dla status = %d, want %d; body=%s", fileResponse.Code, http.StatusOK, fileResponse.Body.String())
	}
	assertBankingConflict(t, router, http.MethodPost, fmt.Sprintf("/api/banking/transactions/%d/file-dla", dlaTxn), nil)

	recodeBody := strings.NewReader(`{"account_code":"6200-software"}`)
	recodeResponse := performBankingRequest(router, http.MethodPost, fmt.Sprintf("/api/banking/transactions/%d/recode", recodeTxn), recodeBody, "application/json", true)
	if recodeResponse.Code != http.StatusOK {
		t.Fatalf("recode status = %d, want %d; body=%s", recodeResponse.Code, http.StatusOK, recodeResponse.Body.String())
	}
	assertBankingConflict(t, router, http.MethodPost, fmt.Sprintf("/api/banking/transactions/%d/recode", recodeTxn), strings.NewReader(`{"account_code":"6200-software"}`))

	excludeResponse := performBankingRequest(router, http.MethodPost, fmt.Sprintf("/api/banking/transactions/%d/exclude", excludeTxn), strings.NewReader(`{"reason":"duplicate export"}`), "application/json", true)
	if excludeResponse.Code != http.StatusOK {
		t.Fatalf("exclude status = %d, want %d; body=%s", excludeResponse.Code, http.StatusOK, excludeResponse.Body.String())
	}
	unexcludeResponse := performBankingRequest(router, http.MethodPost, fmt.Sprintf("/api/banking/transactions/%d/unexclude", excludeTxn), nil, "", true)
	if unexcludeResponse.Code != http.StatusOK {
		t.Fatalf("unexclude status = %d, want %d; body=%s", unexcludeResponse.Code, http.StatusOK, unexcludeResponse.Body.String())
	}
	assertBankingConflict(t, router, http.MethodPost, fmt.Sprintf("/api/banking/transactions/%d/exclude", confirmTxn), strings.NewReader(`{"reason":"too late"}`))
	assertBankingConflict(t, router, http.MethodPost, fmt.Sprintf("/api/banking/transactions/%d/unexclude", confirmTxn), nil)

	feedHTTPResponse := performBankingRequest(router, http.MethodGet, fmt.Sprintf("/api/banking/feed?account=%d&state=reconciled", account.ID), nil, "", true)
	if feedHTTPResponse.Code != http.StatusOK {
		t.Fatalf("feed status = %d, want %d; body=%s", feedHTTPResponse.Code, http.StatusOK, feedHTTPResponse.Body.String())
	}
	var feed feedResponse
	decodeBankingResponse(t, feedHTTPResponse, &feed)
	if len(feed.Transactions) != 3 {
		t.Fatalf("reconciled feed count = %d, want 3; feed=%+v", len(feed.Transactions), feed)
	}

	recentHTTPResponse := performBankingRequest(router, http.MethodGet, "/api/banking/recent", nil, "", true)
	if recentHTTPResponse.Code != http.StatusOK {
		t.Fatalf("recent status = %d, want %d; body=%s", recentHTTPResponse.Code, http.StatusOK, recentHTTPResponse.Body.String())
	}
	var recent recentResponse
	decodeBankingResponse(t, recentHTTPResponse, &recent)
	if len(recent.Transactions) != 3 {
		t.Fatalf("recent count = %d, want 3; recent=%+v", len(recent.Transactions), recent)
	}
	recentForAccountResponse := performBankingRequest(router, http.MethodGet, fmt.Sprintf("/api/banking/recent?account=%d", account.ID), nil, "", true)
	if recentForAccountResponse.Code != http.StatusOK {
		t.Fatalf("recent account status = %d, want %d; body=%s", recentForAccountResponse.Code, http.StatusOK, recentForAccountResponse.Body.String())
	}
	var recentForAccount recentResponse
	decodeBankingResponse(t, recentForAccountResponse, &recentForAccount)
	if len(recentForAccount.Transactions) != 3 {
		t.Fatalf("recent account count = %d, want 3; recent=%+v", len(recentForAccount.Transactions), recentForAccount)
	}
	badRecentResponse := performBankingRequest(router, http.MethodGet, "/api/banking/recent?account=bad", nil, "", true)
	if badRecentResponse.Code != http.StatusBadRequest {
		t.Fatalf("bad recent account status = %d, want %d; body=%s", badRecentResponse.Code, http.StatusBadRequest, badRecentResponse.Body.String())
	}
}

func TestHTTPReceiptUploadGetDeleteAndSurvivesReconciliation(t *testing.T) {
	pool, _ := temporaryMigratedBankingDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	assets := newMemoryReceiptAssetStore()
	ledger := &recordingBankingLedger{}
	service := NewService(
		pool,
		ledger,
		WithLedgerJournal(ledger),
		WithMoneyFX(stubBankingMoneyFX{realised: money.Money{Amount: 0, Currency: "GBP"}}),
		WithInvoicingSettler(stubInvoiceSettler{}),
		WithReceiptAssetStore(assets),
	)
	router := newBankingHTTPTestRouter(t, service)
	account := createBankingHTTPAccount(t, router, "Revolut GBP", "GBP")
	txnID := importSingleBankingTxn(t, ctx, pool, service, AccountID(account.ID), revolutTestTxn{
		Date:      time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC),
		ID:        "http-receipt",
		Payee:     "Receipt Vendor",
		Reference: "INV-RECEIPT",
		Amount:    money.Money{Amount: 9900, Currency: "GBP"},
	})
	mustRecordSuggestion(t, ctx, service, txnID, SuggestionKindInvoiceMatch, 0.980, "invoice-receipt", "invoice match")

	pdf := []byte("%PDF-1.4\n% receipt fixture\n%%EOF\n")
	receiptBody, receiptContentType := multipartReceiptBody(t, "receipt.pdf", "application/pdf", pdf)
	uploadResponse := performBankingRequest(router, http.MethodPut, fmt.Sprintf("/api/banking/transactions/%d/receipt", txnID), receiptBody, receiptContentType, true)
	if uploadResponse.Code != http.StatusOK {
		t.Fatalf("receipt upload status = %d, want %d; body=%s", uploadResponse.Code, http.StatusOK, uploadResponse.Body.String())
	}
	var uploaded receiptResponse
	decodeBankingResponse(t, uploadResponse, &uploaded)
	if uploaded.Filename != "receipt.pdf" || uploaded.ContentType != "application/pdf" || uploaded.Size != int64(len(pdf)) || uploaded.URL == "" {
		t.Fatalf("uploaded receipt = %+v, want filename/content type/size/url", uploaded)
	}

	reviewResponse := performBankingRequest(router, http.MethodGet, "/api/banking/review", nil, "", true)
	if reviewResponse.Code != http.StatusOK {
		t.Fatalf("review status = %d, want %d; body=%s", reviewResponse.Code, http.StatusOK, reviewResponse.Body.String())
	}
	var queue reviewQueueResponse
	decodeBankingResponse(t, reviewResponse, &queue)
	if len(queue.Matches) != 1 || queue.Matches[0].Transaction.Receipt == nil {
		t.Fatalf("review receipt metadata missing: %+v", queue)
	}

	getResponse := performBankingRequest(router, http.MethodGet, fmt.Sprintf("/api/banking/transactions/%d/receipt", txnID), nil, "", true)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("receipt get status = %d, want %d; body=%s", getResponse.Code, http.StatusOK, getResponse.Body.String())
	}
	if got := getResponse.Header().Get("Content-Type"); got != "application/pdf" {
		t.Fatalf("receipt content type = %q, want application/pdf", got)
	}
	if !bytes.Equal(getResponse.Body.Bytes(), pdf) {
		t.Fatalf("receipt bytes = %q, want %q", getResponse.Body.Bytes(), pdf)
	}

	confirmResponse := performBankingRequest(router, http.MethodPost, fmt.Sprintf("/api/banking/transactions/%d/confirm", txnID), nil, "", true)
	if confirmResponse.Code != http.StatusOK {
		t.Fatalf("confirm status = %d, want %d; body=%s", confirmResponse.Code, http.StatusOK, confirmResponse.Body.String())
	}

	recentHTTPResponse := performBankingRequest(router, http.MethodGet, "/api/banking/recent", nil, "", true)
	if recentHTTPResponse.Code != http.StatusOK {
		t.Fatalf("recent status = %d, want %d; body=%s", recentHTTPResponse.Code, http.StatusOK, recentHTTPResponse.Body.String())
	}
	var recent recentResponse
	decodeBankingResponse(t, recentHTTPResponse, &recent)
	if len(recent.Transactions) != 1 || recent.Transactions[0].Transaction.Receipt == nil {
		t.Fatalf("recent receipt metadata missing after reconciliation: %+v", recent)
	}

	getAfterConfirm := performBankingRequest(router, http.MethodGet, fmt.Sprintf("/api/banking/transactions/%d/receipt", txnID), nil, "", true)
	if getAfterConfirm.Code != http.StatusOK || !bytes.Equal(getAfterConfirm.Body.Bytes(), pdf) {
		t.Fatalf("receipt after confirm status/body = %d/%q, want 200/original", getAfterConfirm.Code, getAfterConfirm.Body.String())
	}

	deleteResponse := performBankingRequest(router, http.MethodDelete, fmt.Sprintf("/api/banking/transactions/%d/receipt", txnID), nil, "", true)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("receipt delete status = %d, want %d; body=%s", deleteResponse.Code, http.StatusNoContent, deleteResponse.Body.String())
	}
	getAfterDelete := performBankingRequest(router, http.MethodGet, fmt.Sprintf("/api/banking/transactions/%d/receipt", txnID), nil, "", true)
	if getAfterDelete.Code != http.StatusNotFound {
		t.Fatalf("receipt get after delete status = %d, want %d; body=%s", getAfterDelete.Code, http.StatusNotFound, getAfterDelete.Body.String())
	}
}

func TestHTTPReceiptUploadRejectsUnsupportedTypeAndOversize(t *testing.T) {
	pool, _ := temporaryMigratedBankingDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	service := NewService(pool, &recordingBankingLedger{}, WithReceiptAssetStore(newMemoryReceiptAssetStore()))
	router := newBankingHTTPTestRouter(t, service)
	account := createBankingHTTPAccount(t, router, "Revolut GBP", "GBP")
	txnID := importSingleBankingTxn(t, ctx, pool, service, AccountID(account.ID), revolutTestTxn{
		Date:      time.Date(2026, 12, 2, 9, 0, 0, 0, time.UTC),
		ID:        "http-receipt-invalid",
		Payee:     "Bad Receipt Vendor",
		Reference: "bad receipt",
		Amount:    money.Money{Amount: -1000, Currency: "GBP"},
	})

	badBody, badContentType := multipartReceiptBody(t, "receipt.txt", "text/plain", []byte("not a receipt"))
	badResponse := performBankingRequest(router, http.MethodPut, fmt.Sprintf("/api/banking/transactions/%d/receipt", txnID), badBody, badContentType, true)
	if badResponse.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("unsupported receipt status = %d, want %d; body=%s", badResponse.Code, http.StatusUnsupportedMediaType, badResponse.Body.String())
	}

	oversizedBody, oversizedContentType := multipartReceiptBody(t, "receipt.pdf", "application/pdf", bytes.Repeat([]byte("x"), MaxReceiptBytes+1))
	oversizedResponse := performBankingRequest(router, http.MethodPut, fmt.Sprintf("/api/banking/transactions/%d/receipt", txnID), oversizedBody, oversizedContentType, true)
	if oversizedResponse.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized receipt status = %d, want %d; body=%s", oversizedResponse.Code, http.StatusRequestEntityTooLarge, oversizedResponse.Body.String())
	}
}

func TestBankingOpenAPIFragmentDocumentsHTTPPaths(t *testing.T) {
	t.Parallel()

	document := httpserver.OpenAPIDocument("test", OpenAPIFragment())
	paths, ok := document["paths"].(map[string]any)
	if !ok {
		t.Fatalf("openapi paths missing or wrong type: %+v", document["paths"])
	}
	for _, path := range []string{
		"/api/banking/accounts",
		"/api/banking/accounts/{id}/import",
		"/api/banking/review",
		"/api/banking/feed",
		"/api/banking/recent",
		"/api/banking/payee-rules",
		"/api/banking/payee-rules/{id}",
		"/api/banking/transactions/{id}/confirm",
		"/api/banking/transactions/{id}/file-dla",
		"/api/banking/transactions/{id}/recode",
		"/api/banking/transactions/{id}/exclude",
		"/api/banking/transactions/{id}/unexclude",
		"/api/banking/transactions/{id}/receipt",
	} {
		if _, ok := paths[path]; !ok {
			t.Fatalf("openapi path %s missing from %+v", path, paths)
		}
	}
}

func newBankingHTTPTestRouter(t *testing.T, service *Service) http.Handler {
	t.Helper()

	module := NewHTTPModule(service)
	return httpserver.NewRouter(httpserver.Config{
		Version:          "test",
		DB:               bankingPingerFunc(func(context.Context) error { return nil }),
		APIAuth:          bankingTestAuthMiddleware,
		Modules:          []httpserver.Module{module.HTTPModule()},
		OpenAPIFragments: []httpserver.OpenAPIFragment{module.OpenAPIFragment()},
	})
}

func performBankingRequest(router http.Handler, method string, path string, body io.Reader, contentType string, authenticated bool) *httptest.ResponseRecorder {
	var reader io.Reader
	if body == nil {
		reader = strings.NewReader("")
	} else {
		reader = body
	}
	request := httptest.NewRequest(method, path, reader)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if authenticated {
		request.AddCookie(&http.Cookie{Name: "test_session", Value: "ok"})
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func createBankingHTTPAccount(t *testing.T, router http.Handler, name string, currency string) bankingAccountResponse {
	t.Helper()
	body := strings.NewReader(fmt.Sprintf(`{"name":%q,"provider":"revolut","currency":%q}`, name, currency))
	response := performBankingRequest(router, http.MethodPost, "/api/banking/accounts", body, "application/json", true)
	if response.Code != http.StatusCreated {
		t.Fatalf("create account status = %d, want %d; body=%s", response.Code, http.StatusCreated, response.Body.String())
	}
	var account bankingAccountResponse
	decodeBankingResponse(t, response, &account)
	return account
}

func performBankingCSVImport(t *testing.T, router http.Handler, accountID int64, filename string, data []byte) batchSummaryResponse {
	t.Helper()
	body, contentType := multipartBody(t, filename, data)
	response := performBankingRequest(router, http.MethodPost, fmt.Sprintf("/api/banking/accounts/%d/import", accountID), body, contentType, true)
	if response.Code != http.StatusOK {
		t.Fatalf("import status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	var summary batchSummaryResponse
	decodeBankingResponse(t, response, &summary)
	return summary
}

func multipartBody(t *testing.T, filename string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile(importMultipartFileField, filename)
	if err != nil {
		t.Fatalf("CreateFormFile() error = %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return &body, writer.FormDataContentType()
}

func multipartReceiptBody(t *testing.T, filename string, contentType string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, receiptMultipartFileField, filename))
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatalf("CreatePart() error = %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write multipart receipt: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return &body, writer.FormDataContentType()
}

func decodeBankingResponse(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, response.Body.String())
	}
}

func assertBankingConflict(t *testing.T, router http.Handler, method string, path string, body io.Reader) {
	t.Helper()
	response := performBankingRequest(router, method, path, body, "application/json", true)
	if response.Code != http.StatusConflict {
		t.Fatalf("%s %s status = %d, want %d; body=%s", method, path, response.Code, http.StatusConflict, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != httpserver.ProblemContentType {
		t.Fatalf("%s %s Content-Type = %q, want %s", method, path, got, httpserver.ProblemContentType)
	}
}

func bankingTestAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz", "/readyz", "/api/openapi.json":
			next.ServeHTTP(w, r)
			return
		}
		if _, err := r.Cookie("test_session"); err != nil {
			httpserver.WriteProblem(w, r, httpserver.Problem{
				Type:   "https://ledgerly.local/problems/unauthenticated",
				Title:  http.StatusText(http.StatusUnauthorized),
				Status: http.StatusUnauthorized,
				Detail: "authentication required",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

type bankingPingerFunc func(context.Context) error

func (f bankingPingerFunc) PingContext(ctx context.Context) error {
	return f(ctx)
}

type recordingBankingLedger struct {
	next ledger.EntryID
}

func (r *recordingBankingLedger) EnsureAccount(_ context.Context, _ db.Tx, spec ledger.AccountSpec) (ledger.AccountCode, error) {
	return spec.Code, nil
}

func (r *recordingBankingLedger) Post(_ context.Context, _ db.Tx, _ ledger.NewJournalEntry) (ledger.EntryID, error) {
	r.next++
	return r.next, nil
}

type stubBankingMoneyFX struct {
	realised money.Money
}

func (s stubBankingMoneyFX) ToGBP(_ context.Context, value money.Money, _ time.Time) (money.Money, error) {
	return money.Money{Amount: value.Amount, Currency: "GBP"}, nil
}

func (s stubBankingMoneyFX) RealisedFXAmount(context.Context, db.Tx, string) (money.Money, error) {
	return s.realised, nil
}

type stubInvoiceSettler struct{}

func (stubInvoiceSettler) MarkSettled(context.Context, db.Tx, string, string, time.Time, invoicing.Money) (invoicing.Invoice, error) {
	return invoicing.Invoice{}, nil
}

type recordingDLAFileDrawer struct{}

func (*recordingDLAFileDrawer) FileDrawing(context.Context, db.Tx, dla.TxnRef) error {
	return nil
}

type memoryReceiptAssetStore struct {
	next   int
	assets map[string]ReceiptAsset
}

func newMemoryReceiptAssetStore() *memoryReceiptAssetStore {
	return &memoryReceiptAssetStore{assets: map[string]ReceiptAsset{}}
}

func (s *memoryReceiptAssetStore) StoreReceiptAsset(_ context.Context, upload ReceiptAssetUpload) (string, error) {
	s.next++
	ref := fmt.Sprintf("receipt-asset-%d", s.next)
	s.assets[ref] = ReceiptAsset{
		MIME:  upload.MIME,
		Size:  int64(len(upload.Bytes)),
		Bytes: append([]byte{}, upload.Bytes...),
	}
	return ref, nil
}

func (s *memoryReceiptAssetStore) LoadReceiptAsset(_ context.Context, ref string) (ReceiptAsset, error) {
	asset, ok := s.assets[ref]
	if !ok {
		return ReceiptAsset{}, ErrReceiptNotFound
	}
	asset.Bytes = append([]byte{}, asset.Bytes...)
	return asset, nil
}
