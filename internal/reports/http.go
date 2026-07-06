package reports

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/jurisdiction"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

const (
	problemTypeReportsBadRequest = "https://ledgerly.local/problems/reports/bad-request"
	problemTypeReportsNotFound   = "https://ledgerly.local/problems/reports/not-found"
	maxReportsJSONBodyBytes      = 64 * 1024
)

type reportsHandler struct {
	service *Service
}

type periodResponse struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type moneyResponse struct {
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
}

type plResponse struct {
	Period          periodResponse        `json:"period"`
	TaxYear         string                `json:"tax_year"`
	Income          []incomeLineResponse  `json:"income"`
	IncomeTotal     moneyResponse         `json:"income_total"`
	RealisedFXGains lineItemResponse      `json:"realised_fx_gains"`
	Expenses        []expenseLineResponse `json:"expenses"`
	ExpenseTotal    moneyResponse         `json:"expense_total"`
	ProfitBeforeTax moneyResponse         `json:"profit_before_tax"`
	CorporateTax    taxLineResponse       `json:"corporate_tax"`
	NetProfit       moneyResponse         `json:"net_profit"`
}

type incomeLineResponse struct {
	Label      string        `json:"label"`
	ClientID   string        `json:"client_id"`
	ClientName string        `json:"client_name"`
	Currency   string        `json:"currency"`
	Amount     moneyResponse `json:"amount"`
}

type expenseLineResponse struct {
	AccountCode string        `json:"account_code"`
	AccountName string        `json:"account_name"`
	Amount      moneyResponse `json:"amount"`
}

type lineItemResponse struct {
	Label  string        `json:"label"`
	Amount moneyResponse `json:"amount"`
}

type taxLineResponse struct {
	Label   string        `json:"label"`
	TaxYear string        `json:"tax_year"`
	Rate    string        `json:"rate"`
	Amount  moneyResponse `json:"amount"`
}

type vatResponse struct {
	Period      periodResponse `json:"period"`
	Box1        moneyResponse  `json:"box1"`
	Box4        moneyResponse  `json:"box4"`
	Box6        moneyResponse  `json:"box6"`
	NetPosition moneyResponse  `json:"net_position"`
}

type filingCalendarResponse struct {
	Filings []filingResponse `json:"filings"`
}

type filingResponse struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	Authority string `json:"authority"`
	DueDate   string `json:"due_date"`
	DaysUntil int    `json:"days_until"`
	Status    string `json:"status"`
}

type profitYTDResponse struct {
	TaxYear string        `json:"tax_year"`
	Profit  moneyResponse `json:"profit"`
}

type archiveRefResponse struct {
	URL         string `json:"url"`
	SHA256      string `json:"sha256"`
	SizeBytes   int64  `json:"size_bytes"`
	DataVersion string `json:"data_version"`
	GeneratedAt string `json:"generated_at"`
}

type shareRequestBody struct {
	Email  string         `json:"email"`
	Period periodResponse `json:"period"`
	From   string         `json:"from,omitempty"`
	To     string         `json:"to,omitempty"`
}

type shareResponse struct {
	Status  string             `json:"status"`
	Archive archiveRefResponse `json:"archive"`
	Message string             `json:"message"`
}

// RegisterRoutes mounts reports REST endpoints.
func (m *Module) RegisterRoutes(r chi.Router) {
	h := reportsHandler{service: m.service}
	r.Get("/pl", h.getPL)
	r.Get("/vat", h.getVAT)
	r.Get("/calendar", h.getCalendar)
	r.Get("/profit-ytd", h.getProfitYTD)
	r.Get("/export", h.getExport)
	r.Post("/share", h.shareExport)
}

func (h reportsHandler) getPL(w nethttp.ResponseWriter, r *nethttp.Request) {
	period, err := parseReportsPeriod(r)
	if err != nil {
		writeReportsBadRequest(w, r, err)
		return
	}
	pl, err := h.service.ProfitAndLoss(r.Context(), period)
	if err != nil {
		writeReportsError(w, r, err)
		return
	}
	writeReportsJSON(w, nethttp.StatusOK, plToResponse(pl))
}

func (h reportsHandler) getVAT(w nethttp.ResponseWriter, r *nethttp.Request) {
	period, err := parseQuarterQuery(r)
	if err != nil {
		writeReportsBadRequest(w, r, err)
		return
	}
	figures, err := h.service.VATReturn(r.Context(), period)
	if err != nil {
		writeReportsError(w, r, err)
		return
	}
	writeReportsJSON(w, nethttp.StatusOK, vatToResponse(figures))
}

func (h reportsHandler) getCalendar(w nethttp.ResponseWriter, r *nethttp.Request) {
	calendar, err := h.service.FilingCalendarContext(r.Context())
	if err != nil {
		writeReportsError(w, r, err)
		return
	}
	writeReportsJSON(w, nethttp.StatusOK, calendarToResponse(calendar))
}

func (h reportsHandler) getProfitYTD(w nethttp.ResponseWriter, r *nethttp.Request) {
	taxYear := strings.TrimSpace(r.URL.Query().Get("taxYear"))
	if taxYear == "" {
		writeReportsBadRequest(w, r, fmt.Errorf("taxYear is required"))
		return
	}
	profit, err := h.service.ProfitYTD(r.Context(), taxYear)
	if err != nil {
		writeReportsError(w, r, err)
		return
	}
	writeReportsJSON(w, nethttp.StatusOK, profitYTDResponse{
		TaxYear: taxYear,
		Profit:  moneyToResponse(profit),
	})
}

func (h reportsHandler) getExport(w nethttp.ResponseWriter, r *nethttp.Request) {
	period, err := parseReportsPeriod(r)
	if err != nil {
		writeReportsBadRequest(w, r, err)
		return
	}
	ref, err := h.service.ExportPack(r.Context(), period)
	if err != nil {
		writeReportsError(w, r, err)
		return
	}
	nethttp.Redirect(w, r, ref.URL, nethttp.StatusFound)
}

func (h reportsHandler) shareExport(w nethttp.ResponseWriter, r *nethttp.Request) {
	var body shareRequestBody
	if err := decodeReportsJSON(w, r, &body); err != nil {
		writeReportsBadRequest(w, r, err)
		return
	}
	period, err := body.period()
	if err != nil {
		writeReportsBadRequest(w, r, err)
		return
	}
	result, err := h.service.ShareExportPack(r.Context(), ShareRequest{
		Email:  body.Email,
		Period: period,
	})
	if err != nil {
		writeReportsError(w, r, err)
		return
	}
	writeReportsJSON(w, nethttp.StatusOK, shareResponse{
		Status:  string(result.Status),
		Archive: archiveRefToResponse(result.Archive),
		Message: result.Message,
	})
}

func parseReportsPeriod(r *nethttp.Request) (Period, error) {
	query := r.URL.Query()
	from, err := parseRequiredDateQuery(query.Get("from"), "from")
	if err != nil {
		return Period{}, err
	}
	to, err := parseRequiredDateQuery(query.Get("to"), "to")
	if err != nil {
		return Period{}, err
	}
	return Period{From: from, To: to}, nil
}

func (b shareRequestBody) period() (Period, error) {
	fromText := b.Period.From
	toText := b.Period.To
	if strings.TrimSpace(fromText) == "" {
		fromText = b.From
	}
	if strings.TrimSpace(toText) == "" {
		toText = b.To
	}
	from, err := parseRequiredDateQuery(fromText, "period.from")
	if err != nil {
		return Period{}, err
	}
	to, err := parseRequiredDateQuery(toText, "period.to")
	if err != nil {
		return Period{}, err
	}
	return Period{From: from, To: to}, nil
}

func parseRequiredDateQuery(value string, name string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, fmt.Errorf("%s is required", name)
	}
	parsed, err := time.ParseInLocation(time.DateOnly, value, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be a date in YYYY-MM-DD format", name)
	}
	return parsed, nil
}

func parseQuarterQuery(r *nethttp.Request) (Period, error) {
	value := strings.TrimSpace(r.URL.Query().Get("period"))
	if value == "" {
		return Period{}, fmt.Errorf("period is required")
	}
	return parseQuarterPeriod(value)
}

func parseQuarterPeriod(value string) (Period, error) {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	yearText, quarterText, ok := strings.Cut(normalized, "-Q")
	if !ok || len(yearText) != 4 || len(quarterText) != 1 {
		return Period{}, fmt.Errorf("period must look like YYYY-QN")
	}
	year, err := strconv.Atoi(yearText)
	if err != nil {
		return Period{}, fmt.Errorf("period year must be numeric")
	}
	quarter, err := strconv.Atoi(quarterText)
	if err != nil || quarter < 1 || quarter > 4 {
		return Period{}, fmt.Errorf("period quarter must be 1, 2, 3, or 4")
	}
	from := time.Date(year, time.Month((quarter-1)*3+1), 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 3, -1)
	return Period{From: from, To: to}, nil
}

func plToResponse(pl PL) plResponse {
	response := plResponse{
		Period:          periodToResponse(pl.Period),
		TaxYear:         pl.TaxYear,
		Income:          make([]incomeLineResponse, 0, len(pl.Income)),
		IncomeTotal:     moneyToResponse(pl.IncomeTotal),
		RealisedFXGains: lineItemToResponse(pl.RealisedFXGains),
		Expenses:        make([]expenseLineResponse, 0, len(pl.Expenses)),
		ExpenseTotal:    moneyToResponse(pl.ExpenseTotal),
		ProfitBeforeTax: moneyToResponse(pl.ProfitBeforeTax),
		CorporateTax: taxLineResponse{
			Label:   pl.CorporateTax.Label,
			TaxYear: pl.CorporateTax.TaxYear,
			Rate:    string(pl.CorporateTax.Rate),
			Amount:  moneyToResponse(pl.CorporateTax.Amount),
		},
		NetProfit: moneyToResponse(pl.NetProfit),
	}
	for _, line := range pl.Income {
		response.Income = append(response.Income, incomeLineResponse{
			Label:      line.Label,
			ClientID:   line.ClientID,
			ClientName: line.ClientName,
			Currency:   line.Currency,
			Amount:     moneyToResponse(line.Amount),
		})
	}
	for _, line := range pl.Expenses {
		response.Expenses = append(response.Expenses, expenseLineResponse{
			AccountCode: string(line.AccountCode),
			AccountName: line.AccountName,
			Amount:      moneyToResponse(line.Amount),
		})
	}
	return response
}

func vatToResponse(figures VATFigures) vatResponse {
	return vatResponse{
		Period:      periodToResponse(figures.Period),
		Box1:        moneyToResponse(figures.Box1),
		Box4:        moneyToResponse(figures.Box4),
		Box6:        moneyToResponse(figures.Box6),
		NetPosition: moneyToResponse(figures.NetPosition),
	}
}

func calendarToResponse(calendar []Filing) filingCalendarResponse {
	response := filingCalendarResponse{Filings: make([]filingResponse, 0, len(calendar))}
	for _, filing := range calendar {
		response.Filings = append(response.Filings, filingResponse{
			Key:       filing.Key,
			Label:     filing.Label,
			Authority: filing.Authority,
			DueDate:   filing.DueDate.UTC().Format(time.DateOnly),
			DaysUntil: filing.DaysUntil,
			Status:    string(filing.Status),
		})
	}
	return response
}

func periodToResponse(period Period) periodResponse {
	return periodResponse{
		From: period.From.UTC().Format(time.DateOnly),
		To:   period.To.UTC().Format(time.DateOnly),
	}
}

func lineItemToResponse(item LineItem) lineItemResponse {
	return lineItemResponse{
		Label:  item.Label,
		Amount: moneyToResponse(item.Amount),
	}
}

func moneyToResponse(amount money.Money) moneyResponse {
	return moneyResponse{
		AmountMinor: amount.Amount,
		Currency:    amount.Currency,
	}
}

func archiveRefToResponse(ref ArchiveRef) archiveRefResponse {
	return archiveRefResponse{
		URL:         ref.URL,
		SHA256:      ref.SHA256,
		SizeBytes:   ref.Size,
		DataVersion: ref.DataVersion,
		GeneratedAt: ref.GeneratedAt.UTC().Format(time.RFC3339),
	}
}

func decodeReportsJSON(w nethttp.ResponseWriter, r *nethttp.Request, target any) error {
	r.Body = nethttp.MaxBytesReader(w, r.Body, maxReportsJSONBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("request body must contain a single JSON object")
	}
	return nil
}

func writeReportsError(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	var unknownTaxYear jurisdiction.UnknownTaxYearError
	if errors.Is(err, ErrInvalidPeriod) || errors.Is(err, ErrInvalidTaxYear) || errors.Is(err, ErrInvalidShare) {
		writeReportsBadRequest(w, r, err)
		return
	}
	if errors.As(err, &unknownTaxYear) {
		writeReportsBadRequest(w, r, err)
		return
	}
	if errors.Is(err, identity.ErrProfileNotFound) {
		httpserver.WriteProblem(w, r, httpserver.Problem{
			Type:   problemTypeReportsNotFound,
			Title:  nethttp.StatusText(nethttp.StatusNotFound),
			Status: nethttp.StatusNotFound,
			Detail: "company profile was not found",
		})
		return
	}
	httpserver.WriteError(w, r, err)
}

func writeReportsBadRequest(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	httpserver.WriteProblem(w, r, httpserver.Problem{
		Type:   problemTypeReportsBadRequest,
		Title:  nethttp.StatusText(nethttp.StatusBadRequest),
		Status: nethttp.StatusBadRequest,
		Detail: err.Error(),
	})
}

func writeReportsJSON(w nethttp.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		panic(err)
	}
}
