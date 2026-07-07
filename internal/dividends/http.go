package dividends

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	nethttp "net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/moneyfx/money"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

const (
	maxDividendJSONBodyBytes = 64 * 1024

	problemTypeDividendBadRequest      = "https://ledgerly.local/problems/dividends/bad-request"
	problemTypeDeclarationNotFound     = "https://ledgerly.local/problems/dividends/declaration-not-found"
	problemTypeDeclarationInvalid      = "https://ledgerly.local/problems/dividends/declaration-invalid"
	problemTypeDocumentNotFound        = "https://ledgerly.local/problems/dividends/document-not-found"
	problemTypeDividendValidation      = "https://ledgerly.local/problems/dividends/validation"
	problemTypeDividendPayloadTooLarge = "https://ledgerly.local/problems/dividends/payload-too-large"
)

var errDividendRequestBodyTooLarge = errors.New("request body too large")

type declarationHandler struct {
	service *Service
}

type dividendFieldError struct {
	Pointer string `json:"pointer"`
	Detail  string `json:"detail"`
}

type dividendAmountRequest struct {
	Amount money.Money `json:"amount"`
}

type dividendHistoryResponse struct {
	Declarations []Declaration `json:"declarations"`
}

type validationStripResponse struct {
	Amount             money.Money                   `json:"amount"`
	Headroom           HeadroomBreakdown             `json:"headroom"`
	WithinHeadroom     bool                          `json:"within_headroom"`
	Distributable      bool                          `json:"distributable"`
	DistributableTotal money.Money                   `json:"distributable_total"`
	Withholding        WithholdingValidation         `json:"withholding"`
	PersonalTax        personalTaxValidationResponse `json:"personal_tax"`
}

type personalTaxValidationResponse struct {
	TaxYear      string      `json:"tax_year"`
	PriorYTD     money.Money `json:"prior_ytd"`
	WithDividend money.Money `json:"with_dividend"`
	Marginal     money.Money `json:"marginal"`
	Message      string      `json:"message"`
}

// RegisterRoutes mounts dividends REST endpoints.
func (m *Module) RegisterRoutes(r chi.Router) {
	h := declarationHandler{service: m.service}
	r.Get("/headroom", h.getHeadroom)
	r.Post("/validate", h.validateAmount)
	r.Post("/declare", h.declareAmount)
	r.Get("/history", h.getHistory)
	r.Get("/{id}/voucher", h.getVoucherPDF)
	r.Get("/{id}/minutes", h.getMinutesPDF)
	r.Get("/declarations/{id}/print", h.getDeclarationDocumentPayload)
	r.Post("/declarations/{id}/documents/render", h.renderDeclarationDocuments)
	r.Get("/declarations/{id}/voucher", h.getVoucherPDF)
	r.Get("/declarations/{id}/minutes", h.getMinutesPDF)
}

func (h declarationHandler) getHeadroom(w nethttp.ResponseWriter, r *nethttp.Request) {
	headroom, err := h.service.Headroom(r.Context())
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	writeDeclarationJSON(w, nethttp.StatusOK, headroom)
}

func (h declarationHandler) validateAmount(w nethttp.ResponseWriter, r *nethttp.Request) {
	var request dividendAmountRequest
	if err := decodeDividendJSON(w, r, &request); err != nil {
		writeDividendDecodeError(w, r, err)
		return
	}

	result, err := h.service.Validate(r.Context(), request.Amount)
	if err != nil && !isBlockingValidationResult(err) {
		writeDividendAmountError(w, r, err)
		return
	}
	writeDeclarationJSON(w, nethttp.StatusOK, validationStripToResponse(result))
}

func (h declarationHandler) declareAmount(w nethttp.ResponseWriter, r *nethttp.Request) {
	var request dividendAmountRequest
	if err := decodeDividendJSON(w, r, &request); err != nil {
		writeDividendDecodeError(w, r, err)
		return
	}

	declaration, err := h.service.Declare(r.Context(), request.Amount)
	if err != nil {
		writeDividendAmountError(w, r, err)
		return
	}
	writeDeclarationJSON(w, nethttp.StatusCreated, declaration)
}

func (h declarationHandler) getHistory(w nethttp.ResponseWriter, r *nethttp.Request) {
	declarations, err := h.service.History(r.Context())
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	writeDeclarationJSON(w, nethttp.StatusOK, dividendHistoryResponse{Declarations: declarations})
}

func (h declarationHandler) getDeclarationDocumentPayload(w nethttp.ResponseWriter, r *nethttp.Request) {
	payload, err := h.service.DeclarationDocumentPayload(r.Context(), declarationIDParam(r))
	if err != nil {
		writeDeclarationError(w, r, err)
		return
	}
	writeDeclarationJSON(w, nethttp.StatusOK, payload)
}

func (h declarationHandler) renderDeclarationDocuments(w nethttp.ResponseWriter, r *nethttp.Request) {
	declaration, err := h.service.RenderDeclarationDocumentsNow(r.Context(), declarationIDParam(r))
	if err != nil {
		writeDeclarationError(w, r, err)
		return
	}
	writeDeclarationJSON(w, nethttp.StatusOK, declaration)
}

func (h declarationHandler) getVoucherPDF(w nethttp.ResponseWriter, r *nethttp.Request) {
	declaration, err := h.service.Declaration(r.Context(), declarationIDParam(r))
	if err != nil {
		writeDeclarationError(w, r, err)
		return
	}
	if declaration.VoucherAsset == nil || strings.TrimSpace(string(*declaration.VoucherAsset)) == "" {
		writeMissingDocument(w, r, "/voucher_asset", "dividend voucher PDF asset was not found")
		return
	}
	nethttp.Redirect(w, r, assetURL(*declaration.VoucherAsset), nethttp.StatusFound)
}

func (h declarationHandler) getMinutesPDF(w nethttp.ResponseWriter, r *nethttp.Request) {
	declaration, err := h.service.Declaration(r.Context(), declarationIDParam(r))
	if err != nil {
		writeDeclarationError(w, r, err)
		return
	}
	if declaration.MinutesAsset == nil || strings.TrimSpace(string(*declaration.MinutesAsset)) == "" {
		writeMissingDocument(w, r, "/minutes_asset", "board minutes PDF asset was not found")
		return
	}
	nethttp.Redirect(w, r, assetURL(*declaration.MinutesAsset), nethttp.StatusFound)
}

func declarationIDParam(r *nethttp.Request) DeclarationID {
	return DeclarationID(strings.TrimSpace(chi.URLParam(r, "id")))
}

func assetURL(id identity.AssetID) string {
	return "/api/identity/assets/" + strings.TrimSpace(string(id))
}

func writeDeclarationJSON(w nethttp.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func decodeDividendJSON(w nethttp.ResponseWriter, r *nethttp.Request, target any) error {
	r.Body = nethttp.MaxBytesReader(w, r.Body, maxDividendJSONBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return errDividendRequestBodyTooLarge
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return errors.New("request body is required")
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain a single JSON object")
	}
	return nil
}

func writeDividendDecodeError(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	if errors.Is(err, errDividendRequestBodyTooLarge) {
		httpserver.WriteProblem(w, r, httpserver.Problem{
			Type:   problemTypeDividendPayloadTooLarge,
			Title:  "Dividend request body is too large",
			Status: nethttp.StatusRequestEntityTooLarge,
			Detail: "request body exceeds the dividend JSON payload limit",
		})
		return
	}
	httpserver.WriteProblem(w, r, httpserver.Problem{
		Type:   problemTypeDividendBadRequest,
		Title:  "Malformed dividend request",
		Status: nethttp.StatusBadRequest,
		Detail: err.Error(),
	})
}

func writeDividendAmountError(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	var overHeadroom *OverHeadroomError
	var nonDistributable *NonDistributableYearError
	switch {
	case errors.As(err, &overHeadroom):
		writeDividendValidation(w, r, "Dividend exceeds headroom", err.Error(), []dividendFieldError{
			{Pointer: "/amount", Detail: "exceeds distributable reserves"},
		}, overHeadroom.Distributable)
	case errors.As(err, &nonDistributable):
		writeDividendValidation(w, r, "Dividend cannot be declared", err.Error(), []dividendFieldError{
			{Pointer: "/amount", Detail: "no distributable reserves are available"},
		}, nonDistributable.Distributable)
	case errors.Is(err, ErrNonPositiveAmount):
		writeDividendValidation(w, r, "Dividend amount is invalid", err.Error(), []dividendFieldError{
			{Pointer: "/amount/amount", Detail: "must be positive"},
		}, money.Money{Amount: 0, Currency: gbpCurrency})
	case errors.Is(err, ErrInvalidDeclaration):
		writeDividendValidation(w, r, "Dividend declaration is invalid", err.Error(), []dividendFieldError{
			{Pointer: "/amount", Detail: "is invalid"},
		}, money.Money{Amount: 0, Currency: gbpCurrency})
	default:
		httpserver.WriteError(w, r, err)
	}
}

func writeDividendValidation(
	w nethttp.ResponseWriter,
	r *nethttp.Request,
	title string,
	detail string,
	fields []dividendFieldError,
	distributable money.Money,
) {
	httpserver.WriteProblem(w, r, httpserver.Problem{
		Type:   problemTypeDividendValidation,
		Title:  title,
		Status: nethttp.StatusUnprocessableEntity,
		Detail: detail,
		Extensions: map[string]any{
			"errors":              fields,
			"distributable_total": distributable,
		},
	})
}

func writeMissingDocument(w nethttp.ResponseWriter, r *nethttp.Request, pointer string, detail string) {
	httpserver.WriteProblem(w, r, httpserver.Problem{
		Type:   problemTypeDocumentNotFound,
		Title:  "Dividend document PDF not found",
		Status: nethttp.StatusNotFound,
		Detail: detail,
		Extensions: map[string]any{
			"errors": []dividendFieldError{{Pointer: pointer, Detail: "is not available yet"}},
		},
	})
}

func writeDeclarationError(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	switch {
	case errors.Is(err, ErrDeclarationNotFound):
		httpserver.WriteProblem(w, r, httpserver.Problem{
			Type:   problemTypeDeclarationNotFound,
			Title:  "Dividend declaration not found",
			Status: nethttp.StatusNotFound,
			Detail: "dividend declaration was not found",
		})
	case errors.Is(err, ErrInvalidDeclaration):
		httpserver.WriteProblem(w, r, httpserver.Problem{
			Type:   problemTypeDeclarationInvalid,
			Title:  "Dividend declaration cannot be rendered",
			Status: nethttp.StatusUnprocessableEntity,
			Detail: err.Error(),
		})
	default:
		httpserver.WriteError(w, r, err)
	}
}

func isBlockingValidationResult(err error) bool {
	return errors.Is(err, ErrOverHeadroom) || errors.Is(err, ErrNonDistributableYear)
}

func validationStripToResponse(result ValidationResult) validationStripResponse {
	return validationStripResponse{
		Amount:             result.Amount,
		Headroom:           result.Headroom,
		WithinHeadroom:     result.WithinHeadroom,
		Distributable:      result.Distributable,
		DistributableTotal: result.DistributableTotal,
		Withholding:        result.Withholding,
		PersonalTax: personalTaxValidationResponse{
			TaxYear:      result.PersonalTax.TaxYear,
			PriorYTD:     result.PersonalTax.PriorYTD,
			WithDividend: result.PersonalTax.WithDividend,
			Marginal:     result.PersonalTax.Marginal,
			Message:      result.PersonalTax.Message,
		},
	}
}

func (id DeclarationID) String() string {
	return string(id)
}
