package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/syntheticinc/bytebrew/engine/internal/domain"
	svcschematemplate "github.com/syntheticinc/bytebrew/engine/internal/service/schematemplate"
	ucschematemplate "github.com/syntheticinc/bytebrew/engine/internal/usecase/schematemplate"
)

// SchemaTemplateResponse is the wire shape for a catalog entry returned by
// GET /api/v1/schema-templates. Mirrors the YAML layout so the admin UI
// consumes the same structure in prototype + production modes.
type SchemaTemplateResponse struct {
	Name        string                          `json:"name"`
	Display     string                          `json:"display"`
	Description string                          `json:"description"`
	Category    string                          `json:"category"`
	Icon        string                          `json:"icon,omitempty"`
	Version     string                          `json:"version"`
	Definition  domain.SchemaTemplateDefinition `json:"definition"`
}

// SchemaTemplateListResponse is the envelope for list endpoints, mirroring
// the MCP catalog `{version, servers}` shape.
type SchemaTemplateListResponse struct {
	Version   string                    `json:"version"`
	Templates []SchemaTemplateResponse  `json:"templates"`
}

// ForkTemplateRequest is the body for POST /api/v1/schema-templates/:name/fork.
type ForkTemplateRequest struct {
	SchemaName string `json:"schema_name"`
}

// ForkTemplateResponse is the success envelope for the fork action. The
// admin UI uses SchemaID to navigate to the new schema detail page.
type ForkTemplateResponse struct {
	SchemaID   string            `json:"schema_id"`
	SchemaName string            `json:"schema_name"`
	AgentIDs   map[string]string `json:"agent_ids"`
}

// SchemaTemplateUsecase is the consumer-side contract the handler needs.
// Implemented by usecase/schematemplate.Usecase.
type SchemaTemplateUsecase interface {
	List(ctx context.Context, category, query string) ([]domain.SchemaTemplate, error)
	GetByName(ctx context.Context, name string) (*domain.SchemaTemplate, error)
	ForkTemplate(ctx context.Context, templateName, newSchemaName string) (*ucschematemplate.ForkResult, error)
}

// SchemaTemplateHandler serves /api/v1/schema-templates.
type SchemaTemplateHandler struct {
	uc      SchemaTemplateUsecase
	version string
}

// NewSchemaTemplateHandler constructs the handler. `version` is returned as
// the top-level envelope field on list responses (mirrors the MCP catalog).
func NewSchemaTemplateHandler(uc SchemaTemplateUsecase, version string) *SchemaTemplateHandler {
	if version == "" {
		version = "1.0"
	}
	return &SchemaTemplateHandler{uc: uc, version: version}
}

// List handles GET /api/v1/schema-templates.
//
// Query params:
//
//	?category=support|sales|internal|generic
//	?q=<search term>  (wins over category when both are set)
func (h *SchemaTemplateHandler) List(w http.ResponseWriter, r *http.Request) {
	category := r.URL.Query().Get("category")
	query := r.URL.Query().Get("q")

	items, err := h.uc.List(r.Context(), category, query)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	resp := SchemaTemplateListResponse{
		Version:   h.version,
		Templates: toResponses(items),
	}
	writeJSON(w, http.StatusOK, resp)
}

// Get handles GET /api/v1/schema-templates/:name.
func (h *SchemaTemplateHandler) Get(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, "template name is required")
		return
	}
	t, err := h.uc.GetByName(r.Context(), name)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if t == nil {
		writeJSONError(w, http.StatusNotFound, "template not found")
		return
	}
	writeJSON(w, http.StatusOK, toResponse(*t))
}

// Fork handles POST /api/v1/schema-templates/:name/fork.
func (h *SchemaTemplateHandler) Fork(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, "template name is required")
		return
	}

	var req ForkTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	req.SchemaName = strings.TrimSpace(req.SchemaName)
	if req.SchemaName == "" {
		writeJSONError(w, http.StatusBadRequest, "schema_name is required")
		return
	}

	// Tenant scope is read inside the service via
	// domain.TenantIDFromContext(ctx) — same pattern as every other
	// tenant-aware handler in this package.
	forked, err := h.uc.ForkTemplate(r.Context(), name, req.SchemaName)
	switch {
	case errors.Is(err, svcschematemplate.ErrTemplateNotFound):
		writeJSONError(w, http.StatusNotFound, "template not found")
		return
	case errors.Is(err, svcschematemplate.ErrSchemaNameTaken):
		writeJSONError(w, http.StatusConflict, "schema name already taken")
		return
	case errors.Is(err, svcschematemplate.ErrInvalidTemplate):
		writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
		return
	case err != nil:
		writeDomainError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, ForkTemplateResponse{
		SchemaID:   forked.SchemaID,
		SchemaName: forked.SchemaName,
		AgentIDs:   forked.AgentIDs,
	})
}

func toResponse(t domain.SchemaTemplate) SchemaTemplateResponse {
	return SchemaTemplateResponse{
		Name:        t.Name,
		Display:     t.Display,
		Description: t.Description,
		Category:    string(t.Category),
		Icon:        t.Icon,
		Version:     t.Version,
		Definition:  t.Definition,
	}
}

func toResponses(items []domain.SchemaTemplate) []SchemaTemplateResponse {
	out := make([]SchemaTemplateResponse, len(items))
	for i, t := range items {
		out[i] = toResponse(t)
	}
	return out
}
