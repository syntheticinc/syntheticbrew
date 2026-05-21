package http

import (
	"net/http"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// CatalogProvider provides MCP catalog operations.
type CatalogProvider interface {
	List() []domain.MCPCatalogEntry
	ListByCategory(category domain.MCPCatalogCategory) []domain.MCPCatalogEntry
	Search(query string) []domain.MCPCatalogEntry
	Version() string
}

// CatalogHandler serves GET /api/v1/mcp/catalog.
type CatalogHandler struct {
	catalog CatalogProvider
}

// NewCatalogHandler creates a new CatalogHandler.
func NewCatalogHandler(catalog CatalogProvider) *CatalogHandler {
	return &CatalogHandler{catalog: catalog}
}

// catalogListResponse is the API response for listing catalog entries.
type catalogListResponse struct {
	Version string                    `json:"version"`
	Servers []domain.MCPCatalogEntry  `json:"servers"`
}

// ListCatalog handles GET /api/v1/mcp/catalog
func (h *CatalogHandler) ListCatalog(w http.ResponseWriter, r *http.Request) {
	category := r.URL.Query().Get("category")
	query := r.URL.Query().Get("q")

	var entries []domain.MCPCatalogEntry

	switch {
	case query != "":
		entries = h.catalog.Search(query)
	case category != "":
		entries = h.catalog.ListByCategory(domain.MCPCatalogCategory(category))
	default:
		entries = h.catalog.List()
	}

	if entries == nil {
		entries = []domain.MCPCatalogEntry{}
	}

	writeJSON(w, http.StatusOK, catalogListResponse{
		Version: h.catalog.Version(),
		Servers: entries,
	})
}
