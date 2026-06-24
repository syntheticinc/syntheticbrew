package http

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// KnowledgeBaseInfo is the API response for a knowledge base.
type KnowledgeBaseInfo struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Description      string   `json:"description,omitempty"`
	EmbeddingModelID string   `json:"embedding_model_id,omitempty"`
	FileCount        int      `json:"file_count"`
	LinkedAgents     []string `json:"linked_agents"` // agent names
	CreatedAt        string   `json:"created_at"`
	UpdatedAt        string   `json:"updated_at"`
}

// CreateKBRequest is the request body for creating a knowledge base.
type CreateKBRequest struct {
	Name             string `json:"name"`
	Description      string `json:"description,omitempty"`
	EmbeddingModelID string `json:"embedding_model_id"`
}

// UpdateKBRequest is the request body for PUT /api/v1/knowledge-bases/{id} (full replace).
// name is required for PUT; missing required fields return 400.
type UpdateKBRequest struct {
	Name             string `json:"name"`
	Description      string `json:"description,omitempty"`
	EmbeddingModelID string `json:"embedding_model_id"`
}

// PatchKBRequest is the request body for PATCH /api/v1/knowledge-bases/{id}.
// All fields are pointers: nil means "preserve existing value".
type PatchKBRequest struct {
	Name             *string `json:"name,omitempty"`
	Description      *string `json:"description,omitempty"`
	EmbeddingModelID *string `json:"embedding_model_id,omitempty"`
}

// KBStore provides CRUD for knowledge bases.
type KBStore interface {
	Create(ctx context.Context, name, description, embeddingModelID, tenantID string) (*KnowledgeBaseInfo, error)
	Update(ctx context.Context, id, name, description, embeddingModelID string) (*KnowledgeBaseInfo, error)
	Patch(ctx context.Context, id string, req PatchKBRequest) (*KnowledgeBaseInfo, error)
	GetByID(ctx context.Context, id string) (*KnowledgeBaseInfo, error)
	List(ctx context.Context) ([]KnowledgeBaseInfo, error)
	Delete(ctx context.Context, id string) error
	LinkAgent(ctx context.Context, kbID, agentName string) error
	UnlinkAgent(ctx context.Context, kbID, agentName string) error
}

// KBFileManager provides file operations on a knowledge base.
type KBFileManager interface {
	ListFiles(ctx context.Context, kbID string) ([]KnowledgeFileResponse, error)
	GetFile(ctx context.Context, kbID, fileID string) (*KnowledgeFileResponse, error)
	UploadFile(ctx context.Context, tenantID, kbID, embeddingModelID, fileName, fileType string, fileSize int64, fileHash string, content []byte) (*KnowledgeFileResponse, error)
	DeleteFile(ctx context.Context, kbID, fileID string) error
	DeleteAllFiles(ctx context.Context, kbID string) error
}

// KnowledgeBaseHandler serves /api/v1/knowledge-bases endpoints.
//
// Engine 1.1.0 made the URL `{name}` segment a stable operator-facing handle
// (was UUID in 1.0.x). The handler resolves the name to a tenant-scoped UUID
// via KBNameResolver before invoking the underlying store. Internal IDs
// (file_id) remain UUID.
type KnowledgeBaseHandler struct {
	store       KBStore
	fileManager KBFileManager
	resolver    KBNameResolver
}

// NewKnowledgeBaseHandler creates a new handler.
//
// resolver is the tenant-scoped name → UUID resolver — required to translate
// the URL `{name}` segment into the canonical KB UUID consumed by the
// underlying KBStore and KBFileManager.
func NewKnowledgeBaseHandler(store KBStore, fileManager KBFileManager, resolver KBNameResolver) *KnowledgeBaseHandler {
	return &KnowledgeBaseHandler{store: store, fileManager: fileManager, resolver: resolver}
}

// List handles GET /api/v1/knowledge-bases.
func (h *KnowledgeBaseHandler) List(w http.ResponseWriter, r *http.Request) {
	kbs, err := h.store.List(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if kbs == nil {
		kbs = []KnowledgeBaseInfo{}
	}
	writeJSON(w, http.StatusOK, kbs)
}

// resolveKBName translates the `{name}` URL param into a tenant-scoped UUID.
// On any error it writes the appropriate HTTP response and returns ("", false);
// callers must not write further output when ok == false.
func (h *KnowledgeBaseHandler) resolveKBName(w http.ResponseWriter, r *http.Request) (string, bool) {
	name := chi.URLParam(r, "name")
	id, err := resolveKBNameToUUID(r.Context(), h.resolver, name)
	if err != nil {
		writeNameLookupError(r.Context(), w, "knowledge base", name, err)
		return "", false
	}
	return id, true
}

// Get handles GET /api/v1/knowledge-bases/{id}.
func (h *KnowledgeBaseHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveKBName(w, r)
	if !ok {
		return
	}
	kb, err := h.store.GetByID(r.Context(), id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if kb == nil {
		writeJSONError(w, http.StatusNotFound, "knowledge base not found")
		return
	}
	writeJSON(w, http.StatusOK, kb)
}

// Create handles POST /api/v1/knowledge-bases.
func (h *KnowledgeBaseHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateKBRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeJSONError(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := ValidateResourceName(req.Name); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid knowledge base name: "+err.Error())
		return
	}

	tenantID := domain.TenantIDFromContext(r.Context())
	if tenantID == "" {
		tenantID = domain.CETenantID
	}

	kb, err := h.store.Create(r.Context(), req.Name, req.Description, req.EmbeddingModelID, tenantID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, kb)
}

// Update handles PUT /api/v1/knowledge-bases/{name}.
// PUT is a full-replace: name is required; missing required fields return 400.
// Renaming is forbidden — supplying a name that differs from the URL handle
// returns 409 Conflict (immutability gate). Use PATCH for partial updates.
func (h *KnowledgeBaseHandler) Update(w http.ResponseWriter, r *http.Request) {
	currentName := chi.URLParam(r, "name")
	id, ok := h.resolveKBName(w, r)
	if !ok {
		return
	}
	var req UpdateKBRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeJSONError(w, http.StatusBadRequest, "name is required for PUT (full replace); use PATCH for partial updates")
		return
	}
	if req.Name != currentName {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "name is immutable; recreate with new name and migrate consumers",
		})
		return
	}

	kb, err := h.store.Update(r.Context(), id, req.Name, req.Description, req.EmbeddingModelID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if kb == nil {
		writeJSONError(w, http.StatusNotFound, "knowledge base not found")
		return
	}
	writeJSON(w, http.StatusOK, kb)
}

// PatchKB handles PATCH /api/v1/knowledge-bases/{name}.
// Only non-nil fields are applied; all others preserve their current value.
// Supplying `name` is allowed only when it equals the current URL handle —
// any rename returns 409 Conflict (immutability gate).
func (h *KnowledgeBaseHandler) PatchKB(w http.ResponseWriter, r *http.Request) {
	currentName := chi.URLParam(r, "name")
	id, ok := h.resolveKBName(w, r)
	if !ok {
		return
	}
	var req PatchKBRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name != nil && *req.Name != currentName {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "name is immutable; recreate with new name and migrate consumers",
		})
		return
	}

	kb, err := h.store.Patch(r.Context(), id, req)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if kb == nil {
		writeJSONError(w, http.StatusNotFound, "knowledge base not found")
		return
	}
	writeJSON(w, http.StatusOK, kb)
}

// Delete handles DELETE /api/v1/knowledge-bases/{id}.
func (h *KnowledgeBaseHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := h.resolveKBName(w, r)
	if !ok {
		return
	}

	existing, err := h.store.GetByID(r.Context(), id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if existing == nil {
		writeJSONError(w, http.StatusNotFound, "knowledge base not found")
		return
	}

	// Delete all files first
	if h.fileManager != nil {
		if err := h.fileManager.DeleteAllFiles(r.Context(), id); err != nil {
			writeDomainError(w, err)
			return
		}
	}

	if err := h.store.Delete(r.Context(), id); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// LinkAgent handles POST /api/v1/knowledge-bases/{id}/agents/{agent_name}.
func (h *KnowledgeBaseHandler) LinkAgent(w http.ResponseWriter, r *http.Request) {
	kbID, ok := h.resolveKBName(w, r)
	if !ok {
		return
	}
	agentName := chi.URLParam(r, "agent_name")
	if kbID == "" || agentName == "" {
		writeJSONError(w, http.StatusBadRequest, "kb id and agent_name are required")
		return
	}
	if err := h.store.LinkAgent(r.Context(), kbID, agentName); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "linked"})
}

// UnlinkAgent handles DELETE /api/v1/knowledge-bases/{id}/agents/{agent_name}.
func (h *KnowledgeBaseHandler) UnlinkAgent(w http.ResponseWriter, r *http.Request) {
	kbID, ok := h.resolveKBName(w, r)
	if !ok {
		return
	}
	agentName := chi.URLParam(r, "agent_name")
	if kbID == "" || agentName == "" {
		writeJSONError(w, http.StatusBadRequest, "kb id and agent_name are required")
		return
	}
	if err := h.store.UnlinkAgent(r.Context(), kbID, agentName); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "unlinked"})
}

// GetFile handles GET /api/v1/knowledge-bases/{id}/files/{file_id}.
func (h *KnowledgeBaseHandler) GetFile(w http.ResponseWriter, r *http.Request) {
	kbID, ok := h.resolveKBName(w, r)
	if !ok {
		return
	}
	fileID := chi.URLParam(r, "file_id")
	if fileID == "" {
		writeJSONError(w, http.StatusBadRequest, "file_id is required")
		return
	}
	if h.fileManager == nil {
		writeJSONError(w, http.StatusNotImplemented, "Knowledge indexing requires an embedding model.")
		return
	}
	file, err := h.fileManager.GetFile(r.Context(), kbID, fileID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if file == nil {
		writeJSONError(w, http.StatusNotFound, "file not found")
		return
	}
	writeJSON(w, http.StatusOK, file)
}

// ListFiles handles GET /api/v1/knowledge-bases/{id}/files.
func (h *KnowledgeBaseHandler) ListFiles(w http.ResponseWriter, r *http.Request) {
	kbID, ok := h.resolveKBName(w, r)
	if !ok {
		return
	}

	existing, err := h.store.GetByID(r.Context(), kbID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if existing == nil {
		writeJSONError(w, http.StatusNotFound, "knowledge base not found")
		return
	}

	if h.fileManager == nil {
		writeJSONError(w, http.StatusNotImplemented, "Knowledge indexing requires an embedding model. Configure one in Models → select type Embeddings.")
		return
	}
	files, err := h.fileManager.ListFiles(r.Context(), kbID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if files == nil {
		files = []KnowledgeFileResponse{}
	}
	writeJSON(w, http.StatusOK, files)
}

// UploadFile handles POST /api/v1/knowledge-bases/{id}/files.
func (h *KnowledgeBaseHandler) UploadFile(w http.ResponseWriter, r *http.Request) {
	kbID, ok := h.resolveKBName(w, r)
	if !ok {
		return
	}
	if h.fileManager == nil {
		writeJSONError(w, http.StatusNotImplemented, "Knowledge indexing requires an embedding model. Configure one in Models → select type Embeddings.")
		return
	}

	// Resolve KB to get embedding model ID
	kb, err := h.store.GetByID(r.Context(), kbID)
	if err != nil || kb == nil {
		writeJSONError(w, http.StatusNotFound, "knowledge base not found")
		return
	}
	if kb.EmbeddingModelID == "" {
		writeJSONError(w, http.StatusBadRequest, "no embedding model configured for this knowledge base")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize+1024)
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		writeJSONError(w, http.StatusBadRequest, "file too large or invalid multipart form (max 50MB)")
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()

	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "file field is required")
		return
	}
	defer func() { _ = file.Close() }()

	originalName := sanitizeUploadFilename(header.Filename)
	if originalName == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid filename")
		return
	}

	ext := strings.ToLower(filepath.Ext(originalName))
	allowedMIME, ok := allowedMIMETypes[ext]
	if !ok {
		writeJSONError(w, http.StatusBadRequest,
			fmt.Sprintf("unsupported file type %q, supported: txt, md, csv, pdf, docx", ext))
		return
	}

	if !validateMIME(header, allowedMIME) {
		writeJSONError(w, http.StatusBadRequest,
			fmt.Sprintf("file content type does not match extension %q", ext))
		return
	}

	content, err := io.ReadAll(file)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "failed to read file")
		return
	}
	if len(content) == 0 {
		writeJSONError(w, http.StatusBadRequest, "empty file")
		return
	}

	fileHash := fmt.Sprintf("%x", sha256.Sum256(content))
	fileType := strings.TrimPrefix(ext, ".")

	tenantID := domain.TenantIDFromContext(r.Context())
	if tenantID == "" {
		tenantID = domain.CETenantID
	}

	resp, err := h.fileManager.UploadFile(r.Context(), tenantID, kbID, kb.EmbeddingModelID, originalName, fileType, int64(len(content)), fileHash, content)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

// DeleteFile handles DELETE /api/v1/knowledge-bases/{id}/files/{file_id}.
func (h *KnowledgeBaseHandler) DeleteFile(w http.ResponseWriter, r *http.Request) {
	kbID, ok := h.resolveKBName(w, r)
	if !ok {
		return
	}
	fileID := chi.URLParam(r, "file_id")
	if h.fileManager == nil {
		writeJSONError(w, http.StatusNotImplemented, "Knowledge indexing requires an embedding model. Configure one in Models → select type Embeddings.")
		return
	}
	existing, err := h.store.GetByID(r.Context(), kbID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if existing == nil {
		writeJSONError(w, http.StatusNotFound, "knowledge base not found")
		return
	}
	if err := h.fileManager.DeleteFile(r.Context(), kbID, fileID); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

