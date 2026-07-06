package dividends

import (
	"encoding/json"
	"errors"
	nethttp "net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/npmulder/ledgerly/internal/identity"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

const (
	problemTypeDeclarationNotFound = "https://ledgerly.local/problems/dividends/declaration-not-found"
	problemTypeDeclarationInvalid  = "https://ledgerly.local/problems/dividends/declaration-invalid"
	problemTypeDocumentNotFound    = "https://ledgerly.local/problems/dividends/document-not-found"
)

type declarationHandler struct {
	service *Service
}

type dividendFieldError struct {
	Pointer string `json:"pointer"`
	Detail  string `json:"detail"`
}

// RegisterRoutes mounts dividends REST endpoints.
func (m *Module) RegisterRoutes(r chi.Router) {
	h := declarationHandler{service: m.service}
	r.Get("/declarations/{id}/print", h.getDeclarationDocumentPayload)
	r.Post("/declarations/{id}/documents/render", h.renderDeclarationDocuments)
	r.Get("/declarations/{id}/voucher", h.getVoucherPDF)
	r.Get("/declarations/{id}/minutes", h.getMinutesPDF)
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

func (id DeclarationID) String() string {
	return string(id)
}
