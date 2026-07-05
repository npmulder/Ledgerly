package demo

import (
	"encoding/json"
	"errors"
	nethttp "net/http"

	"github.com/go-chi/chi/v5"

	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

const problemTypeInvalidNote = "https://ledgerly.local/problems/demo/invalid-note"

type handler struct {
	service *Service
}

type createNoteRequest struct {
	Body string `json:"body"`
}

type listNotesResponse struct {
	Notes []Note `json:"notes"`
}

// RegisterRoutes mounts the demo REST endpoints.
func (m *Module) RegisterRoutes(r chi.Router) {
	h := handler{service: m.service}
	r.Post("/notes", h.createNote)
	r.Get("/notes", h.listNotes)
}

func (h handler) createNote(w nethttp.ResponseWriter, r *nethttp.Request) {
	var request createNoteRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		httpserver.WriteProblem(w, r, invalidNoteProblem("request body must be JSON with a body string"))
		return
	}

	note, err := h.service.CreateNote(r.Context(), CreateNoteInput(request))
	if err != nil {
		writeDemoError(w, r, err)
		return
	}

	writeJSON(w, nethttp.StatusCreated, note)
}

func (h handler) listNotes(w nethttp.ResponseWriter, r *nethttp.Request) {
	notes, err := h.service.ListNotes(r.Context())
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}

	writeJSON(w, nethttp.StatusOK, listNotesResponse{Notes: notes})
}

func writeDemoError(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	if errors.Is(err, ErrEmptyNoteBody) {
		httpserver.WriteProblem(w, r, invalidNoteProblem("body is required"))
		return
	}
	httpserver.WriteError(w, r, err)
}

func invalidNoteProblem(detail string) httpserver.Problem {
	return httpserver.Problem{
		Type:   problemTypeInvalidNote,
		Title:  nethttp.StatusText(nethttp.StatusBadRequest),
		Status: nethttp.StatusBadRequest,
		Detail: detail,
	}
}

func writeJSON(w nethttp.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		panic(err)
	}
}

func openAPIFragment() httpserver.OpenAPIFragment {
	return httpserver.OpenAPIFragment{
		Tags: []map[string]any{
			{"name": "demo", "description": "Walking-skeleton demo module"},
		},
		Paths: map[string]any{
			"/api/demo/notes": map[string]any{
				"get": map[string]any{
					"tags":        []string{"demo"},
					"summary":     "List demo notes",
					"operationId": "demoListNotes",
					"responses": map[string]any{
						"200": map[string]any{
							"description": "Notes listed",
						},
					},
				},
				"post": map[string]any{
					"tags":        []string{"demo"},
					"summary":     "Create a demo note",
					"operationId": "demoCreateNote",
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"$ref": "#/components/schemas/DemoCreateNoteRequest"},
							},
						},
					},
					"responses": map[string]any{
						"201": map[string]any{
							"description": "Note created",
						},
						"400": map[string]any{
							"description": "Invalid note request",
						},
					},
				},
			},
		},
		Components: map[string]any{
			"schemas": map[string]any{
				"DemoCreateNoteRequest": map[string]any{
					"type":     "object",
					"required": []string{"body"},
					"properties": map[string]any{
						"body": map[string]any{"type": "string"},
					},
				},
				"DemoNote": map[string]any{
					"type":     "object",
					"required": []string{"id", "body", "created_at"},
					"properties": map[string]any{
						"id":         map[string]any{"type": "string"},
						"body":       map[string]any{"type": "string"},
						"created_at": map[string]any{"type": "string", "format": "date-time"},
					},
				},
			},
		},
	}
}
