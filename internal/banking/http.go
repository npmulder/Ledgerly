package banking

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	nethttp "net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/npmulder/ledgerly/internal/ledger"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

const (
	maxBankingJSONBodyBytes       = 64 * 1024
	maxImportCSVBytes             = 10 * 1024 * 1024
	maxImportMultipartBodyBytes   = maxImportCSVBytes + 1024*1024
	maxReceiptMultipartBodyBytes  = MaxReceiptBytes + 32*1024
	importMultipartFileField      = "file"
	receiptMultipartFileField     = "receipt"
	bankingFeedPageSize           = DefaultFeedLimit
	defaultUnexcludeReason        = "manual unexclude"
	problemTypeBankingBadRequest  = "https://ledgerly.local/problems/banking/bad-request"
	problemTypeBankingConflict    = "https://ledgerly.local/problems/banking/conflict"
	problemTypeBankingNotFound    = "https://ledgerly.local/problems/banking/not-found"
	problemTypeBankingPayload     = "https://ledgerly.local/problems/banking/payload-too-large"
	problemTypeBankingUnsupported = "https://ledgerly.local/problems/banking/unsupported-media-type"
	problemTypeBankingValidation  = "https://ledgerly.local/problems/banking/validation"
)

var errBankingRequestBodyTooLarge = errors.New("banking: request body too large")

type bankingHandler struct {
	service *Service
}

type createAccountRequest struct {
	Name     string   `json:"name"`
	Provider Provider `json:"provider"`
	Currency string   `json:"currency"`
}

type bankingAccountsResponse struct {
	Accounts []bankingAccountResponse `json:"accounts"`
}

type payeeRulesResponse struct {
	Rules []payeeRuleResponse `json:"rules"`
}

type bankingAccountResponse struct {
	ID                int64    `json:"id"`
	Name              string   `json:"name"`
	Provider          Provider `json:"provider"`
	Currency          string   `json:"currency"`
	LedgerAccountCode string   `json:"ledger_account_code"`
	UnreconciledCount int      `json:"unreconciled_count"`
	CreatedAt         string   `json:"created_at"`
}

type batchSummaryResponse struct {
	BatchID    int64  `json:"batch_id"`
	AccountID  int64  `json:"account_id"`
	Filename   string `json:"filename"`
	ImportedAt string `json:"imported_at"`
	Total      int    `json:"total"`
	New        int    `json:"new"`
	Duplicates int    `json:"duplicates"`
}

type moneyResponse struct {
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
}

type transactionResponse struct {
	ID            int64             `json:"id"`
	AccountID     int64             `json:"account_id"`
	Date          string            `json:"date"`
	Amount        moneyResponse     `json:"amount"`
	Payee         string            `json:"payee"`
	Reference     string            `json:"reference"`
	ProviderMeta  map[string]string `json:"provider_meta"`
	ImportBatchID int64             `json:"import_batch_id"`
	State         TransactionState  `json:"state"`
	CreatedAt     string            `json:"created_at"`
	Receipt       *receiptResponse  `json:"receipt"`
}

type receiptResponse struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	UploadedAt  string `json:"uploaded_at"`
	URL         string `json:"url"`
}

type reviewQueueResponse struct {
	Matches     []reviewCardResponse `json:"matches"`
	Suggestions []reviewCardResponse `json:"suggestions"`
	Rules       []reviewCardResponse `json:"rules"`
}

type reviewCardResponse struct {
	Kind         string               `json:"kind"`
	SuggestionID int64                `json:"suggestion_id"`
	Transaction  transactionResponse  `json:"transaction"`
	Confidence   float64              `json:"confidence"`
	Explanation  string               `json:"explanation"`
	Target       reviewTargetResponse `json:"target"`
}

type reviewTargetResponse struct {
	Type          string `json:"type"`
	ID            string `json:"id,omitempty"`
	InvoiceNumber string `json:"invoice_number,omitempty"`
	Client        string `json:"client,omitempty"`
	AccountCode   string `json:"account_code,omitempty"`
	TimesApplied  *int   `json:"times_applied,omitempty"`
}

type feedResponse struct {
	Transactions []transactionResponse `json:"transactions"`
	NextCursor   *string               `json:"next_cursor"`
}

type feedCursorToken struct {
	Date string        `json:"date"`
	ID   TransactionID `json:"id"`
}

type recentResponse struct {
	Transactions []recentTransactionResponse `json:"transactions"`
}

type invoiceCandidatesResponse struct {
	Candidates []invoiceCandidateResponse `json:"candidates"`
}

type invoiceCandidateResponse struct {
	InvoiceID     string        `json:"invoice_id"`
	InvoiceNumber string        `json:"invoice_number"`
	Client        string        `json:"client"`
	IssueDate     string        `json:"issue_date"`
	DueDate       string        `json:"due_date"`
	Amount        moneyResponse `json:"amount"`
	Status        string        `json:"status"`
}

type recentTransactionResponse struct {
	Transaction  transactionResponse `json:"transaction"`
	ReconciledAt string              `json:"reconciled_at"`
	Actor        string              `json:"actor"`
}

type commandResponse struct {
	Transaction      *transactionResponse `json:"transaction,omitempty"`
	Kind             string               `json:"kind,omitempty"`
	RealisedFXAmount *moneyResponse       `json:"realised_fx_amount,omitempty"`
	AmountGBP        *moneyResponse       `json:"amount_gbp,omitempty"`
	Rule             *payeeRuleResponse   `json:"rule,omitempty"`
	StateChange      *stateChangeResponse `json:"state_change,omitempty"`
}

type payeeRuleResponse struct {
	ID            int64   `json:"id"`
	Matcher       string  `json:"matcher"`
	MatchMode     string  `json:"match_mode"`
	AccountCode   string  `json:"account_code"`
	TimesApplied  int     `json:"times_applied"`
	LastAppliedAt *string `json:"last_applied_at"`
	CreatedFrom   string  `json:"created_from"`
	CreatedAt     string  `json:"created_at"`
}

type stateChangeResponse struct {
	ID            int64            `json:"id"`
	TransactionID int64            `json:"transaction_id"`
	From          TransactionState `json:"from"`
	To            TransactionState `json:"to"`
	ChangedAt     string           `json:"changed_at"`
	Actor         string           `json:"actor"`
}

type recodeRequest struct {
	AccountCode      string `json:"account_code"`
	AccountCodeCamel string `json:"accountCode"`
}

type confirmMatchRequest struct {
	InvoiceID      string `json:"invoice_id"`
	InvoiceIDCamel string `json:"invoiceId"`
}

type payeeRuleRequest struct {
	Matcher     string `json:"matcher"`
	MatchMode   string `json:"match_mode"`
	AccountCode string `json:"account_code"`
}

type reasonRequest struct {
	Reason string `json:"reason"`
}

type bankingFieldError struct {
	Pointer string `json:"pointer"`
	Detail  string `json:"detail"`
	Row     *int   `json:"row,omitempty"`
}

// RegisterRoutes mounts banking REST endpoints.
func (m *Module) RegisterRoutes(r chi.Router) {
	h := bankingHandler{service: m.service}
	r.Get("/accounts", h.listAccounts)
	r.Post("/accounts", h.createAccount)
	r.Post("/accounts/{id}/import", h.importAccountCSV)
	r.Get("/review", h.getReviewQueue)
	r.Get("/feed", h.getFeed)
	r.Get("/recent", h.getRecent)
	r.Get("/payee-rules", h.listPayeeRules)
	r.Post("/payee-rules", h.createPayeeRule)
	r.Put("/payee-rules/{id}", h.updatePayeeRule)
	r.Delete("/payee-rules/{id}", h.deletePayeeRule)
	r.Get("/transactions/{id}/invoice-candidates", h.listInvoiceCandidates)
	r.Post("/transactions/{id}/confirm", h.confirmTransaction)
	r.Post("/transactions/{id}/file-dla", h.fileTransactionToDLA)
	r.Post("/transactions/{id}/recode", h.recodeTransaction)
	r.Post("/transactions/{id}/exclude", h.excludeTransaction)
	r.Post("/transactions/{id}/unexclude", h.unexcludeTransaction)
	r.Put("/transactions/{id}/receipt", h.putReceipt)
	r.Get("/transactions/{id}/receipt", h.getReceipt)
	r.Delete("/transactions/{id}/receipt", h.deleteReceipt)
}

func (h bankingHandler) listAccounts(w nethttp.ResponseWriter, r *nethttp.Request) {
	if h.service == nil {
		httpserver.WriteError(w, r, errors.New("banking: service is required"))
		return
	}
	accounts, err := h.service.Accounts(r.Context())
	if err != nil {
		writeBankingError(w, r, err)
		return
	}
	response := bankingAccountsResponse{Accounts: make([]bankingAccountResponse, 0, len(accounts))}
	for _, account := range accounts {
		count, err := h.service.UnreconciledCount(r.Context(), account.ID)
		if err != nil {
			writeBankingError(w, r, err)
			return
		}
		response.Accounts = append(response.Accounts, accountToResponse(account, count))
	}
	writeBankingJSON(w, nethttp.StatusOK, response)
}

func (h bankingHandler) createAccount(w nethttp.ResponseWriter, r *nethttp.Request) {
	if h.service == nil {
		httpserver.WriteError(w, r, errors.New("banking: service is required"))
		return
	}
	var request createAccountRequest
	if err := decodeBankingJSON(w, r, &request); err != nil {
		writeBankingDecodeError(w, r, err)
		return
	}
	account, err := h.service.CreateAccount(r.Context(), AccountInput(request))
	if err != nil {
		writeBankingError(w, r, err)
		return
	}
	count, err := h.service.UnreconciledCount(r.Context(), account.ID)
	if err != nil {
		writeBankingError(w, r, err)
		return
	}
	writeBankingJSON(w, nethttp.StatusCreated, accountToResponse(account, count))
}

func (h bankingHandler) importAccountCSV(w nethttp.ResponseWriter, r *nethttp.Request) {
	if h.service == nil {
		httpserver.WriteError(w, r, errors.New("banking: service is required"))
		return
	}
	accountID, ok := accountIDParam(w, r)
	if !ok {
		return
	}
	contentType := strings.ToLower(r.Header.Get("Content-Type"))
	if !strings.HasPrefix(contentType, "multipart/form-data") {
		writeBankingProblem(w, r, nethttp.StatusUnsupportedMediaType, problemTypeBankingUnsupported, "multipart/form-data is required")
		return
	}

	r.Body = nethttp.MaxBytesReader(w, r.Body, maxImportMultipartBodyBytes)
	file, header, err := r.FormFile(importMultipartFileField)
	if err != nil {
		if isRequestTooLarge(err) || errors.Is(err, multipart.ErrMessageTooLarge) {
			writeBankingPayloadTooLarge(w, r)
			return
		}
		writeBankingBadRequest(w, r, fmt.Errorf("multipart field %q is required", importMultipartFileField))
		return
	}
	defer func() {
		_ = file.Close()
	}()

	var buf bytes.Buffer
	copied, err := io.Copy(&buf, io.LimitReader(file, maxImportCSVBytes+1))
	if err != nil {
		writeBankingBadRequest(w, r, fmt.Errorf("read CSV upload: %w", err))
		return
	}
	if copied > maxImportCSVBytes {
		writeBankingPayloadTooLarge(w, r)
		return
	}
	filename := strings.TrimSpace(header.Filename)
	if filename == "" {
		filename = "statement.csv"
	}
	summary, err := h.service.ImportCSV(r.Context(), accountID, ImportFile{
		Filename: filename,
		Reader:   bytes.NewReader(buf.Bytes()),
	})
	if err != nil {
		writeBankingError(w, r, err)
		return
	}
	writeBankingJSON(w, nethttp.StatusOK, batchSummaryToResponse(summary))
}

func (h bankingHandler) getReviewQueue(w nethttp.ResponseWriter, r *nethttp.Request) {
	if h.service == nil {
		httpserver.WriteError(w, r, errors.New("banking: service is required"))
		return
	}
	queue, err := h.service.ReviewQueue(r.Context())
	if err != nil {
		writeBankingError(w, r, err)
		return
	}
	response, err := h.reviewQueueToResponse(r.Context(), queue)
	if err != nil {
		writeBankingError(w, r, err)
		return
	}
	writeBankingJSON(w, nethttp.StatusOK, response)
}

func (h bankingHandler) getFeed(w nethttp.ResponseWriter, r *nethttp.Request) {
	if h.service == nil {
		httpserver.WriteError(w, r, errors.New("banking: service is required"))
		return
	}
	filter, err := parseFeedFilter(r)
	if err != nil {
		writeBankingBadRequest(w, r, err)
		return
	}
	filter.Limit = bankingFeedPageSize + 1
	txns, err := h.service.Feed(r.Context(), filter)
	if err != nil {
		writeBankingError(w, r, err)
		return
	}
	var nextCursor *string
	if len(txns) > bankingFeedPageSize {
		cursor := encodeFeedCursor(txns[bankingFeedPageSize-1])
		nextCursor = &cursor
		txns = txns[:bankingFeedPageSize]
	}
	txns, err = h.transactionsWithReceiptMetadata(r.Context(), txns)
	if err != nil {
		writeBankingError(w, r, err)
		return
	}
	writeBankingJSON(w, nethttp.StatusOK, feedToResponse(txns, nextCursor))
}

func (h bankingHandler) getRecent(w nethttp.ResponseWriter, r *nethttp.Request) {
	if h.service == nil {
		httpserver.WriteError(w, r, errors.New("banking: service is required"))
		return
	}
	query := r.URL.Query()
	limit, err := parseOptionalPositiveInt(query.Get("limit"), "limit")
	if err != nil {
		writeBankingBadRequest(w, r, err)
		return
	}
	var accountID AccountID
	if value := strings.TrimSpace(query.Get("account")); value != "" {
		parsed, err := parsePositiveInt64(value, "account")
		if err != nil {
			writeBankingBadRequest(w, r, err)
			return
		}
		accountID = AccountID(parsed)
	}
	recent, err := h.service.RecentlyReconciled(r.Context(), accountID, limit)
	if err != nil {
		writeBankingError(w, r, err)
		return
	}
	recent, err = h.recentWithReceiptMetadata(r.Context(), recent)
	if err != nil {
		writeBankingError(w, r, err)
		return
	}
	writeBankingJSON(w, nethttp.StatusOK, recentToResponse(recent))
}

func (h bankingHandler) listPayeeRules(w nethttp.ResponseWriter, r *nethttp.Request) {
	if h.service == nil {
		httpserver.WriteError(w, r, errors.New("banking: service is required"))
		return
	}
	rules, err := h.service.PayeeRules(r.Context())
	if err != nil {
		writeBankingError(w, r, err)
		return
	}
	response := payeeRulesResponse{Rules: make([]payeeRuleResponse, 0, len(rules))}
	for _, rule := range rules {
		response.Rules = append(response.Rules, payeeRuleToResponse(rule))
	}
	writeBankingJSON(w, nethttp.StatusOK, response)
}

func (h bankingHandler) createPayeeRule(w nethttp.ResponseWriter, r *nethttp.Request) {
	if h.service == nil {
		httpserver.WriteError(w, r, errors.New("banking: service is required"))
		return
	}
	var request payeeRuleRequest
	if err := decodeBankingJSON(w, r, &request); err != nil {
		writeBankingDecodeError(w, r, err)
		return
	}
	rule, err := h.service.CreatePayeeRule(r.Context(), PayeeRuleInput{
		Matcher:     request.Matcher,
		MatchMode:   PayeeRuleMatchMode(request.MatchMode),
		AccountCode: ledger.AccountCode(request.AccountCode),
		CreatedFrom: PayeeRuleCreatedFromManual,
	})
	if err != nil {
		writeBankingError(w, r, err)
		return
	}
	writeBankingJSON(w, nethttp.StatusCreated, payeeRuleToResponse(rule))
}

func (h bankingHandler) updatePayeeRule(w nethttp.ResponseWriter, r *nethttp.Request) {
	if h.service == nil {
		httpserver.WriteError(w, r, errors.New("banking: service is required"))
		return
	}
	ruleID, ok := payeeRuleIDParam(w, r)
	if !ok {
		return
	}
	var request payeeRuleRequest
	if err := decodeBankingJSON(w, r, &request); err != nil {
		writeBankingDecodeError(w, r, err)
		return
	}
	rule, err := h.service.UpdatePayeeRule(r.Context(), ruleID, PayeeRuleUpdateInput{
		Matcher:     request.Matcher,
		MatchMode:   PayeeRuleMatchMode(request.MatchMode),
		AccountCode: ledger.AccountCode(request.AccountCode),
	})
	if err != nil {
		writeBankingError(w, r, err)
		return
	}
	writeBankingJSON(w, nethttp.StatusOK, payeeRuleToResponse(rule))
}

func (h bankingHandler) deletePayeeRule(w nethttp.ResponseWriter, r *nethttp.Request) {
	if h.service == nil {
		httpserver.WriteError(w, r, errors.New("banking: service is required"))
		return
	}
	ruleID, ok := payeeRuleIDParam(w, r)
	if !ok {
		return
	}
	if err := h.service.DeletePayeeRule(r.Context(), ruleID); err != nil {
		writeBankingError(w, r, err)
		return
	}
	w.WriteHeader(nethttp.StatusNoContent)
}

func (h bankingHandler) listInvoiceCandidates(w nethttp.ResponseWriter, r *nethttp.Request) {
	txnID, ok := transactionIDParam(w, r)
	if !ok {
		return
	}
	candidates, err := h.service.InvoiceCandidatesForTransaction(r.Context(), txnID)
	if err != nil {
		writeBankingError(w, r, err)
		return
	}
	writeBankingJSON(w, nethttp.StatusOK, invoiceCandidatesToResponse(candidates))
}

func (h bankingHandler) confirmTransaction(w nethttp.ResponseWriter, r *nethttp.Request) {
	txnID, ok := transactionIDParam(w, r)
	if !ok {
		return
	}
	var invoiceID string
	if requestHasBody(r) {
		var request confirmMatchRequest
		if err := decodeBankingJSON(w, r, &request); err != nil {
			writeBankingDecodeError(w, r, err)
			return
		}
		invoiceID = strings.TrimSpace(request.InvoiceID)
		if invoiceID == "" {
			invoiceID = strings.TrimSpace(request.InvoiceIDCamel)
		}
		if invoiceID == "" {
			writeBankingValidation(w, r, "invoice_id is required", []bankingFieldError{{Pointer: "/invoice_id", Detail: "is required"}})
			return
		}
	}
	result, err := h.service.ConfirmMatchToInvoice(r.Context(), txnID, invoiceID)
	if err != nil {
		writeBankingError(w, r, err)
		return
	}
	realised := moneyToResponse(result.RealisedFXGBP)
	transaction, err := h.transactionToResponse(r.Context(), result.Transaction)
	if err != nil {
		writeBankingError(w, r, err)
		return
	}
	writeBankingJSON(w, nethttp.StatusOK, commandResponse{
		Transaction:      &transaction,
		Kind:             cardKindForSuggestion(result.Kind),
		RealisedFXAmount: &realised,
	})
}

func (h bankingHandler) fileTransactionToDLA(w nethttp.ResponseWriter, r *nethttp.Request) {
	txnID, ok := transactionIDParam(w, r)
	if !ok {
		return
	}
	result, err := h.service.FileToDLA(r.Context(), txnID)
	if err != nil {
		writeBankingError(w, r, err)
		return
	}
	amountGBP := moneyToResponse(result.AmountGBP)
	transaction, err := h.transactionToResponse(r.Context(), result.Transaction)
	if err != nil {
		writeBankingError(w, r, err)
		return
	}
	writeBankingJSON(w, nethttp.StatusOK, commandResponse{
		Transaction: &transaction,
		Kind:        cardKindForSuggestion(result.Kind),
		AmountGBP:   &amountGBP,
	})
}

func (h bankingHandler) recodeTransaction(w nethttp.ResponseWriter, r *nethttp.Request) {
	txnID, ok := transactionIDParam(w, r)
	if !ok {
		return
	}
	var request recodeRequest
	if err := decodeBankingJSON(w, r, &request); err != nil {
		writeBankingDecodeError(w, r, err)
		return
	}
	accountCode := strings.TrimSpace(request.AccountCode)
	if accountCode == "" {
		accountCode = strings.TrimSpace(request.AccountCodeCamel)
	}
	if accountCode == "" {
		writeBankingValidation(w, r, "account_code is required", []bankingFieldError{{Pointer: "/account_code", Detail: "is required"}})
		return
	}
	result, err := h.service.Recode(r.Context(), txnID, ledger.AccountCode(accountCode))
	if err != nil {
		writeBankingError(w, r, err)
		return
	}
	transaction, err := h.transactionToResponse(r.Context(), result.Transaction)
	if err != nil {
		writeBankingError(w, r, err)
		return
	}
	rule := payeeRuleToResponse(result.Rule)
	writeBankingJSON(w, nethttp.StatusOK, commandResponse{
		Transaction: &transaction,
		Kind:        cardKindForSuggestion(result.Kind),
		Rule:        &rule,
	})
}

func (h bankingHandler) excludeTransaction(w nethttp.ResponseWriter, r *nethttp.Request) {
	txnID, ok := transactionIDParam(w, r)
	if !ok {
		return
	}
	var request reasonRequest
	if err := decodeBankingJSON(w, r, &request); err != nil {
		writeBankingDecodeError(w, r, err)
		return
	}
	if strings.TrimSpace(request.Reason) == "" {
		writeBankingValidation(w, r, "exclude reason is required", []bankingFieldError{{Pointer: "/reason", Detail: "is required"}})
		return
	}
	change, err := h.service.Exclude(r.Context(), txnID, request.Reason)
	if err != nil {
		writeBankingError(w, r, err)
		return
	}
	stateChange := stateChangeToResponse(change)
	writeBankingJSON(w, nethttp.StatusOK, commandResponse{StateChange: &stateChange})
}

func (h bankingHandler) putReceipt(w nethttp.ResponseWriter, r *nethttp.Request) {
	txnID, ok := transactionIDParam(w, r)
	if !ok {
		return
	}
	upload, err := readReceiptUpload(w, r)
	if err != nil {
		if isRequestTooLarge(err) {
			writeBankingPayloadTooLarge(w, r)
			return
		}
		writeBankingBadRequest(w, r, err)
		return
	}
	receipt, err := h.service.AttachReceipt(r.Context(), txnID, upload)
	if err != nil {
		writeBankingError(w, r, err)
		return
	}
	writeBankingJSON(w, nethttp.StatusOK, receiptToResponse(txnID, receipt))
}

func (h bankingHandler) getReceipt(w nethttp.ResponseWriter, r *nethttp.Request) {
	txnID, ok := transactionIDParam(w, r)
	if !ok {
		return
	}
	receipt, asset, err := h.service.ReceiptAsset(r.Context(), txnID)
	if err != nil {
		writeBankingError(w, r, err)
		return
	}
	contentType := strings.TrimSpace(asset.MIME)
	if contentType == "" {
		contentType = receipt.MIME
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(int64(len(asset.Bytes)), 10))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": receipt.Filename}))
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.WriteHeader(nethttp.StatusOK)
	_, _ = w.Write(asset.Bytes)
}

func (h bankingHandler) deleteReceipt(w nethttp.ResponseWriter, r *nethttp.Request) {
	txnID, ok := transactionIDParam(w, r)
	if !ok {
		return
	}
	if err := h.service.DeleteReceipt(r.Context(), txnID); err != nil {
		writeBankingError(w, r, err)
		return
	}
	w.WriteHeader(nethttp.StatusNoContent)
}

func (h bankingHandler) unexcludeTransaction(w nethttp.ResponseWriter, r *nethttp.Request) {
	txnID, ok := transactionIDParam(w, r)
	if !ok {
		return
	}
	reason := defaultUnexcludeReason
	if requestHasBody(r) {
		var request reasonRequest
		if err := decodeBankingJSON(w, r, &request); err != nil {
			writeBankingDecodeError(w, r, err)
			return
		}
		if strings.TrimSpace(request.Reason) != "" {
			reason = request.Reason
		}
	}
	change, err := h.service.Unexclude(r.Context(), txnID, reason)
	if err != nil {
		writeBankingError(w, r, err)
		return
	}
	stateChange := stateChangeToResponse(change)
	writeBankingJSON(w, nethttp.StatusOK, commandResponse{StateChange: &stateChange})
}

func accountIDParam(w nethttp.ResponseWriter, r *nethttp.Request) (AccountID, bool) {
	id, err := parsePositiveInt64(chi.URLParam(r, "id"), "account id")
	if err != nil {
		writeBankingBadRequest(w, r, err)
		return 0, false
	}
	return AccountID(id), true
}

func transactionIDParam(w nethttp.ResponseWriter, r *nethttp.Request) (TransactionID, bool) {
	id, err := parsePositiveInt64(chi.URLParam(r, "id"), "transaction id")
	if err != nil {
		writeBankingBadRequest(w, r, err)
		return 0, false
	}
	return TransactionID(id), true
}

func payeeRuleIDParam(w nethttp.ResponseWriter, r *nethttp.Request) (PayeeRuleID, bool) {
	id, err := parsePositiveInt64(chi.URLParam(r, "id"), "payee rule id")
	if err != nil {
		writeBankingBadRequest(w, r, err)
		return 0, false
	}
	return PayeeRuleID(id), true
}

func parseFeedFilter(r *nethttp.Request) (FeedFilter, error) {
	query := r.URL.Query()
	var filter FeedFilter
	if value := strings.TrimSpace(query.Get("account")); value != "" {
		id, err := parsePositiveInt64(value, "account")
		if err != nil {
			return FeedFilter{}, err
		}
		filter.AccountID = AccountID(id)
	}
	if value := strings.TrimSpace(query.Get("state")); value != "" {
		filter.State = TransactionState(value)
	}
	if value := strings.TrimSpace(query.Get("cursor")); value != "" {
		cursor, err := decodeFeedCursor(value)
		if err != nil {
			return FeedFilter{}, err
		}
		filter.After = &cursor
	}
	return filter, nil
}

func parsePositiveInt64(value string, label string) (int64, error) {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", label)
	}
	return parsed, nil
}

func parseOptionalPositiveInt(value string, label string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", label)
	}
	return parsed, nil
}

func encodeFeedCursor(txn Transaction) string {
	payload, err := json.Marshal(feedCursorToken{
		Date: txn.Date.UTC().Format(time.DateOnly),
		ID:   txn.ID,
	})
	if err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeFeedCursor(value string) (FeedCursor, error) {
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return FeedCursor{}, fmt.Errorf("cursor is invalid")
	}
	var token feedCursorToken
	if err := json.Unmarshal(payload, &token); err != nil {
		return FeedCursor{}, fmt.Errorf("cursor is invalid")
	}
	date, err := time.Parse(time.DateOnly, token.Date)
	if err != nil || token.ID <= 0 {
		return FeedCursor{}, fmt.Errorf("cursor is invalid")
	}
	return FeedCursor{Date: date, ID: token.ID}, nil
}

func (h bankingHandler) reviewQueueToResponse(ctx context.Context, queue ReviewQueue) (reviewQueueResponse, error) {
	response := reviewQueueResponse{
		Matches:     make([]reviewCardResponse, 0, len(queue.InvoiceMatches)),
		Suggestions: make([]reviewCardResponse, 0, len(queue.DLA)),
		Rules:       make([]reviewCardResponse, 0, len(queue.PayeeRules)),
	}
	receipts, err := h.service.ReceiptSummaries(ctx, reviewQueueTransactionIDs(queue))
	if err != nil {
		return reviewQueueResponse{}, err
	}
	invoiceCandidates := map[string][]InvoiceMatchCandidate{}
	for _, item := range queue.InvoiceMatches {
		item.Transaction = transactionWithReceiptMetadata(item.Transaction, receipts)
		card, err := h.reviewCardToResponse(ctx, item, invoiceCandidates)
		if err != nil {
			return reviewQueueResponse{}, err
		}
		response.Matches = append(response.Matches, card)
	}
	for _, item := range queue.DLA {
		item.Transaction = transactionWithReceiptMetadata(item.Transaction, receipts)
		card, err := h.reviewCardToResponse(ctx, item, invoiceCandidates)
		if err != nil {
			return reviewQueueResponse{}, err
		}
		response.Suggestions = append(response.Suggestions, card)
	}
	for _, item := range queue.PayeeRules {
		item.Transaction = transactionWithReceiptMetadata(item.Transaction, receipts)
		card, err := h.reviewCardToResponse(ctx, item, invoiceCandidates)
		if err != nil {
			return reviewQueueResponse{}, err
		}
		response.Rules = append(response.Rules, card)
	}
	return response, nil
}

func reviewQueueTransactionIDs(queue ReviewQueue) []TransactionID {
	ids := make([]TransactionID, 0, len(queue.InvoiceMatches)+len(queue.DLA)+len(queue.PayeeRules))
	for _, item := range queue.InvoiceMatches {
		ids = append(ids, item.Transaction.ID)
	}
	for _, item := range queue.DLA {
		ids = append(ids, item.Transaction.ID)
	}
	for _, item := range queue.PayeeRules {
		ids = append(ids, item.Transaction.ID)
	}
	return ids
}

func (h bankingHandler) reviewCardToResponse(ctx context.Context, item ReviewQueueItem, invoiceCandidates map[string][]InvoiceMatchCandidate) (reviewCardResponse, error) {
	target, err := h.reviewTargetToResponse(ctx, item, invoiceCandidates)
	if err != nil {
		return reviewCardResponse{}, err
	}
	return reviewCardResponse{
		Kind:         cardKindForSuggestion(item.Suggestion.Kind),
		SuggestionID: int64(item.Suggestion.ID),
		Transaction:  transactionToResponse(item.Transaction),
		Confidence:   item.Suggestion.Confidence,
		Explanation:  item.Suggestion.Explanation,
		Target:       target,
	}, nil
}

func (h bankingHandler) reviewTargetToResponse(ctx context.Context, item ReviewQueueItem, invoiceCandidates map[string][]InvoiceMatchCandidate) (reviewTargetResponse, error) {
	switch item.Suggestion.Kind {
	case SuggestionKindInvoiceMatch:
		target := reviewTargetResponse{
			Type: "invoice",
			ID:   item.Suggestion.Target,
		}
		candidate, found, err := h.invoiceMatchCandidate(ctx, item, invoiceCandidates)
		if err != nil {
			return reviewTargetResponse{}, err
		}
		if found {
			target.InvoiceNumber = candidate.Number
			target.Client = candidate.ClientName
		}
		return target, nil
	case SuggestionKindDLA:
		return reviewTargetResponse{
			Type: "dla",
			ID:   item.Suggestion.Target,
		}, nil
	case SuggestionKindPayeeRule:
		target := reviewTargetResponse{
			Type:        "account",
			AccountCode: item.Suggestion.Target,
		}
		timesApplied, found, err := h.payeeRuleTimesApplied(ctx, item)
		if err != nil {
			return reviewTargetResponse{}, err
		}
		if found {
			target.TimesApplied = &timesApplied
		}
		return target, nil
	default:
		return reviewTargetResponse{}, fmt.Errorf("banking: review suggestion kind %q: %w", item.Suggestion.Kind, ErrInvalidSuggestion)
	}
}

func (h bankingHandler) invoiceMatchCandidate(ctx context.Context, item ReviewQueueItem, cache map[string][]InvoiceMatchCandidate) (InvoiceMatchCandidate, bool, error) {
	if h.service == nil || h.service.invoiceCandidates == nil {
		return InvoiceMatchCandidate{}, false, nil
	}
	currency := item.Transaction.Amount.Currency
	candidates, ok := cache[currency]
	if !ok {
		var err error
		candidates, err = h.service.invoiceCandidatesForCurrency(ctx, currency)
		if err != nil {
			return InvoiceMatchCandidate{}, false, err
		}
		cache[currency] = candidates
	}
	for _, candidate := range candidates {
		if candidate.InvoiceID == item.Suggestion.Target {
			return candidate, true, nil
		}
	}
	return InvoiceMatchCandidate{}, false, nil
}

func (h bankingHandler) payeeRuleTimesApplied(ctx context.Context, item ReviewQueueItem) (int, bool, error) {
	rules, err := h.service.MatchingPayeeRules(ctx, item.Transaction.Payee)
	if err != nil {
		return 0, false, err
	}
	for _, rule := range rules {
		if string(rule.AccountCode) == item.Suggestion.Target {
			return rule.TimesApplied, true, nil
		}
	}
	return 0, false, nil
}

func accountToResponse(account BankAccount, unreconciledCount int) bankingAccountResponse {
	return bankingAccountResponse{
		ID:                int64(account.ID),
		Name:              account.Name,
		Provider:          account.Provider,
		Currency:          account.Currency,
		LedgerAccountCode: string(account.LedgerAccountCode),
		UnreconciledCount: unreconciledCount,
		CreatedAt:         account.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func batchSummaryToResponse(summary BatchSummary) batchSummaryResponse {
	return batchSummaryResponse{
		BatchID:    int64(summary.BatchID),
		AccountID:  int64(summary.AccountID),
		Filename:   summary.Filename,
		ImportedAt: summary.ImportedAt.UTC().Format(time.RFC3339Nano),
		Total:      summary.TotalRows,
		New:        summary.NewRows,
		Duplicates: summary.DuplicateRows,
	}
}

func transactionToResponse(txn Transaction) transactionResponse {
	providerMeta := txn.ProviderMeta
	if providerMeta == nil {
		providerMeta = map[string]string{}
	}
	var receipt *receiptResponse
	if txn.Receipt != nil {
		receipt = receiptMetadataToResponse(txn.ID, *txn.Receipt)
	}
	return transactionResponse{
		ID:            int64(txn.ID),
		AccountID:     int64(txn.AccountID),
		Date:          txn.Date.UTC().Format(time.DateOnly),
		Amount:        moneyToResponse(txn.Amount),
		Payee:         txn.Payee,
		Reference:     txn.Reference,
		ProviderMeta:  providerMeta,
		ImportBatchID: int64(txn.ImportBatchID),
		State:         txn.State,
		CreatedAt:     txn.CreatedAt.UTC().Format(time.RFC3339Nano),
		Receipt:       receipt,
	}
}

func (h bankingHandler) transactionToResponse(ctx context.Context, txn Transaction) (transactionResponse, error) {
	txns, err := h.transactionsWithReceiptMetadata(ctx, []Transaction{txn})
	if err != nil {
		return transactionResponse{}, err
	}
	if len(txns) == 0 {
		return transactionResponse{}, fmt.Errorf("banking: transaction response is missing: %w", ErrTransactionNotFound)
	}
	return transactionToResponse(txns[0]), nil
}

func (h bankingHandler) transactionsWithReceiptMetadata(ctx context.Context, txns []Transaction) ([]Transaction, error) {
	if len(txns) == 0 {
		return txns, nil
	}
	ids := make([]TransactionID, 0, len(txns))
	for _, txn := range txns {
		ids = append(ids, txn.ID)
	}
	receipts, err := h.service.ReceiptSummaries(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := append([]Transaction{}, txns...)
	for i := range out {
		out[i] = transactionWithReceiptMetadata(out[i], receipts)
	}
	return out, nil
}

func (h bankingHandler) recentWithReceiptMetadata(ctx context.Context, recent []ReconciledTransaction) ([]ReconciledTransaction, error) {
	if len(recent) == 0 {
		return recent, nil
	}
	ids := make([]TransactionID, 0, len(recent))
	for _, item := range recent {
		ids = append(ids, item.Transaction.ID)
	}
	receipts, err := h.service.ReceiptSummaries(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := append([]ReconciledTransaction{}, recent...)
	for i := range out {
		out[i].Transaction = transactionWithReceiptMetadata(out[i].Transaction, receipts)
	}
	return out, nil
}

func transactionWithReceiptMetadata(txn Transaction, receipts map[TransactionID]Receipt) Transaction {
	receipt, ok := receipts[txn.ID]
	if !ok {
		txn.Receipt = nil
		return txn
	}
	meta := receiptToMetadata(receipt)
	txn.Receipt = &meta
	return txn
}

func receiptToMetadata(receipt Receipt) ReceiptMetadata {
	return ReceiptMetadata{
		Filename:   receipt.Filename,
		MIME:       receipt.MIME,
		Size:       receipt.Size,
		UploadedAt: receipt.UploadedAt,
	}
}

func receiptToResponse(txnID TransactionID, receipt Receipt) *receiptResponse {
	return receiptMetadataToResponse(txnID, receiptToMetadata(receipt))
}

func receiptMetadataToResponse(txnID TransactionID, receipt ReceiptMetadata) *receiptResponse {
	return &receiptResponse{
		Filename:    receipt.Filename,
		ContentType: receipt.MIME,
		Size:        receipt.Size,
		UploadedAt:  receipt.UploadedAt.UTC().Format(time.RFC3339Nano),
		URL:         receiptURL(txnID),
	}
}

func receiptURL(txnID TransactionID) string {
	return fmt.Sprintf("/api/banking/transactions/%d/receipt", txnID)
}

func feedToResponse(txns []Transaction, nextCursor *string) feedResponse {
	response := feedResponse{
		Transactions: make([]transactionResponse, 0, len(txns)),
		NextCursor:   nextCursor,
	}
	for _, txn := range txns {
		response.Transactions = append(response.Transactions, transactionToResponse(txn))
	}
	return response
}

func recentToResponse(recent []ReconciledTransaction) recentResponse {
	response := recentResponse{Transactions: make([]recentTransactionResponse, 0, len(recent))}
	for _, item := range recent {
		response.Transactions = append(response.Transactions, recentTransactionResponse{
			Transaction:  transactionToResponse(item.Transaction),
			ReconciledAt: item.ReconciledAt.UTC().Format(time.RFC3339Nano),
			Actor:        item.Actor,
		})
	}
	return response
}

func invoiceCandidatesToResponse(candidates []InvoiceMatchCandidate) invoiceCandidatesResponse {
	response := invoiceCandidatesResponse{Candidates: make([]invoiceCandidateResponse, 0, len(candidates))}
	for _, candidate := range candidates {
		response.Candidates = append(response.Candidates, invoiceCandidateResponse{
			InvoiceID:     candidate.InvoiceID,
			InvoiceNumber: candidate.Number,
			Client:        candidate.ClientName,
			IssueDate:     candidate.IssueDate.UTC().Format(time.DateOnly),
			DueDate:       candidate.DueDate.UTC().Format(time.DateOnly),
			Amount:        moneyToResponse(candidate.Amount),
			Status:        candidate.Status,
		})
	}
	return response
}

func payeeRuleToResponse(rule PayeeRule) payeeRuleResponse {
	var lastAppliedAt *string
	if rule.LastAppliedAt != nil {
		value := rule.LastAppliedAt.UTC().Format(time.RFC3339Nano)
		lastAppliedAt = &value
	}
	return payeeRuleResponse{
		ID:            int64(rule.ID),
		Matcher:       rule.Matcher,
		MatchMode:     string(rule.MatchMode),
		AccountCode:   string(rule.AccountCode),
		TimesApplied:  rule.TimesApplied,
		LastAppliedAt: lastAppliedAt,
		CreatedFrom:   string(rule.CreatedFrom),
		CreatedAt:     rule.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func stateChangeToResponse(change TransactionStateChange) stateChangeResponse {
	return stateChangeResponse{
		ID:            int64(change.ID),
		TransactionID: int64(change.TransactionID),
		From:          change.From,
		To:            change.To,
		ChangedAt:     change.ChangedAt.UTC().Format(time.RFC3339Nano),
		Actor:         change.Actor,
	}
}

func moneyToResponse(value money.Money) moneyResponse {
	return moneyResponse{AmountMinor: value.Amount, Currency: value.Currency}
}

func cardKindForSuggestion(kind SuggestionKind) string {
	switch kind {
	case SuggestionKindInvoiceMatch:
		return "match"
	case SuggestionKindDLA:
		return "suggestion"
	case SuggestionKindPayeeRule:
		return "rule"
	default:
		return string(kind)
	}
}

func readReceiptUpload(w nethttp.ResponseWriter, r *nethttp.Request) (ReceiptUpload, error) {
	r.Body = nethttp.MaxBytesReader(w, r.Body, maxReceiptMultipartBodyBytes)
	reader, err := r.MultipartReader()
	if err != nil {
		return ReceiptUpload{}, fmt.Errorf("receipt upload must be multipart/form-data with a %q file field", receiptMultipartFileField)
	}

	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if isRequestTooLarge(err) || errors.Is(err, multipart.ErrMessageTooLarge) {
				return ReceiptUpload{}, errBankingRequestBodyTooLarge
			}
			return ReceiptUpload{}, fmt.Errorf("read multipart receipt: %w", err)
		}
		if part.FormName() != receiptMultipartFileField {
			_ = part.Close()
			continue
		}
		defer func() {
			_ = part.Close()
		}()

		data, err := io.ReadAll(io.LimitReader(part, MaxReceiptBytes+1))
		if err != nil {
			if isRequestTooLarge(err) {
				return ReceiptUpload{}, errBankingRequestBodyTooLarge
			}
			return ReceiptUpload{}, fmt.Errorf("read receipt upload: %w", err)
		}
		if len(data) > MaxReceiptBytes {
			return ReceiptUpload{}, errBankingRequestBodyTooLarge
		}
		mediaType := strings.TrimSpace(part.Header.Get("Content-Type"))
		if mediaType == "" {
			mediaType = nethttp.DetectContentType(data)
		}
		return ReceiptUpload{
			Filename: part.FileName(),
			MIME:     mediaType,
			Bytes:    data,
		}, nil
	}

	return ReceiptUpload{}, fmt.Errorf("receipt upload must include a %q file field", receiptMultipartFileField)
}

func decodeBankingJSON(w nethttp.ResponseWriter, r *nethttp.Request, target any) error {
	r.Body = nethttp.MaxBytesReader(w, r.Body, maxBankingJSONBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		if isRequestTooLarge(err) {
			return errBankingRequestBodyTooLarge
		}
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("request body is required")
		}
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if isRequestTooLarge(err) {
			return errBankingRequestBodyTooLarge
		}
		return fmt.Errorf("request body must contain a single JSON object")
	}
	return nil
}

func requestHasBody(r *nethttp.Request) bool {
	return r.Body != nil && r.Body != nethttp.NoBody && r.ContentLength != 0
}

func isRequestTooLarge(err error) bool {
	var maxErr *nethttp.MaxBytesError
	return errors.As(err, &maxErr) || errors.Is(err, errBankingRequestBodyTooLarge)
}

func writeBankingDecodeError(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	if isRequestTooLarge(err) {
		writeBankingPayloadTooLarge(w, r)
		return
	}
	writeBankingBadRequest(w, r, err)
}

func writeBankingError(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	if problem, ok := bankingProblemForError(err); ok {
		httpserver.WriteProblem(w, r, problem)
		return
	}
	httpserver.WriteError(w, r, err)
}

func bankingProblemForError(err error) (httpserver.Problem, bool) {
	var rowErr *ParseRowError
	if errors.As(err, &rowErr) {
		return validationProblemWithRows(err.Error(), []int{rowErr.Row}), true
	}
	var mismatch *CurrencyMismatchError
	if errors.As(err, &mismatch) {
		rows := []int{}
		if mismatch.Row > 0 {
			rows = append(rows, mismatch.Row)
		}
		return validationProblemWithRows(err.Error(), rows), true
	}
	switch {
	case errors.Is(err, ErrAlreadyReconciled):
		return httpserver.Problem{
			Type:   problemTypeBankingConflict,
			Title:  nethttp.StatusText(nethttp.StatusConflict),
			Status: nethttp.StatusConflict,
			Detail: err.Error(),
		}, true
	case errors.Is(err, ErrInvalidStateTransition):
		return httpserver.Problem{
			Type:   problemTypeBankingConflict,
			Title:  nethttp.StatusText(nethttp.StatusConflict),
			Status: nethttp.StatusConflict,
			Detail: err.Error(),
		}, true
	case errors.Is(err, ErrAccountNotFound), errors.Is(err, ErrTransactionNotFound), errors.Is(err, ErrSuggestionNotFound), errors.Is(err, ErrPayeeRuleNotFound), errors.Is(err, ErrReceiptNotFound):
		return httpserver.Problem{
			Type:   problemTypeBankingNotFound,
			Title:  nethttp.StatusText(nethttp.StatusNotFound),
			Status: nethttp.StatusNotFound,
			Detail: err.Error(),
		}, true
	case errors.Is(err, ErrReceiptTooLarge):
		return httpserver.Problem{
			Type:   problemTypeBankingPayload,
			Title:  nethttp.StatusText(nethttp.StatusRequestEntityTooLarge),
			Status: nethttp.StatusRequestEntityTooLarge,
			Detail: err.Error(),
		}, true
	case errors.Is(err, ErrUnsupportedReceipt):
		return httpserver.Problem{
			Type:   problemTypeBankingUnsupported,
			Title:  nethttp.StatusText(nethttp.StatusUnsupportedMediaType),
			Status: nethttp.StatusUnsupportedMediaType,
			Detail: err.Error(),
		}, true
	case errors.Is(err, ErrInvalidTransactionFilter):
		return httpserver.Problem{
			Type:   problemTypeBankingBadRequest,
			Title:  nethttp.StatusText(nethttp.StatusBadRequest),
			Status: nethttp.StatusBadRequest,
			Detail: err.Error(),
		}, true
	case errors.Is(err, ErrInvalidAccount),
		errors.Is(err, ErrUnsupportedProvider),
		errors.Is(err, ErrUnsupportedCurrency),
		errors.Is(err, ErrInvalidImport),
		errors.Is(err, ErrCurrencyMismatch),
		errors.Is(err, ErrInvalidSuggestion),
		errors.Is(err, ErrInvalidPayeeRule),
		errors.Is(err, ErrInvalidReconciliation),
		errors.Is(err, ErrInvalidReceipt):
		return httpserver.Problem{
			Type:   problemTypeBankingValidation,
			Title:  nethttp.StatusText(nethttp.StatusUnprocessableEntity),
			Status: nethttp.StatusUnprocessableEntity,
			Detail: err.Error(),
		}, true
	default:
		return httpserver.Problem{}, false
	}
}

func validationProblemWithRows(detail string, rows []int) httpserver.Problem {
	fieldErrors := make([]bankingFieldError, 0, len(rows))
	for _, row := range rows {
		rowCopy := row
		fieldErrors = append(fieldErrors, bankingFieldError{
			Pointer: "/file",
			Detail:  detail,
			Row:     &rowCopy,
		})
	}
	extensions := map[string]any{}
	if len(rows) > 0 {
		extensions["row_numbers"] = rows
		extensions["errors"] = fieldErrors
	}
	return httpserver.Problem{
		Type:       problemTypeBankingValidation,
		Title:      nethttp.StatusText(nethttp.StatusUnprocessableEntity),
		Status:     nethttp.StatusUnprocessableEntity,
		Detail:     detail,
		Extensions: extensions,
	}
}

func writeBankingValidation(w nethttp.ResponseWriter, r *nethttp.Request, detail string, fieldErrors []bankingFieldError) {
	httpserver.WriteProblem(w, r, httpserver.Problem{
		Type:   problemTypeBankingValidation,
		Title:  nethttp.StatusText(nethttp.StatusUnprocessableEntity),
		Status: nethttp.StatusUnprocessableEntity,
		Detail: detail,
		Extensions: map[string]any{
			"errors": fieldErrors,
		},
	})
}

func writeBankingBadRequest(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	writeBankingProblem(w, r, nethttp.StatusBadRequest, problemTypeBankingBadRequest, err.Error())
}

func writeBankingPayloadTooLarge(w nethttp.ResponseWriter, r *nethttp.Request) {
	writeBankingProblem(w, r, nethttp.StatusRequestEntityTooLarge, problemTypeBankingPayload, "request body is too large")
}

func writeBankingProblem(w nethttp.ResponseWriter, r *nethttp.Request, status int, problemType string, detail string) {
	httpserver.WriteProblem(w, r, httpserver.Problem{
		Type:   problemType,
		Title:  nethttp.StatusText(status),
		Status: status,
		Detail: detail,
	})
}

func writeBankingJSON(w nethttp.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		panic(err)
	}
}
