package http

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// KnowledgeStats provides knowledge base statistics for an agent.
type KnowledgeStats interface {
	GetStats(ctx context.Context, agentName string) (docCount int, chunkCount int, lastIndexed *time.Time, err error)
}

// KnowledgeFileLister lists knowledge files for an agent (AC-KB-LIST-01..04).
type KnowledgeFileLister interface {
	ListFiles(ctx context.Context, agentName string) ([]KnowledgeFileResponse, error)
	DeleteFile(ctx context.Context, agentName, fileID string) error
}

// KnowledgeFileResponse represents a knowledge file in the API response (AC-KB-LIST-02).
type KnowledgeFileResponse struct {
	ID         string `json:"id"`
	FileName   string `json:"file_name"`
	FileType   string `json:"file_type"`
	FileSize   int64  `json:"file_size"`
	Status     string `json:"status"` // uploading, indexing, ready, error
	StatusMsg  string `json:"status_message,omitempty"`
	ChunkCount int    `json:"chunk_count"`
	CreatedAt  string `json:"created_at"`
	IndexedAt  string `json:"indexed_at,omitempty"`
}

// KnowledgeHandler serves /api/v1/agents/{name}/knowledge endpoints.
type KnowledgeHandler struct {
	stats        KnowledgeStats
	fileLister   KnowledgeFileLister
	fileUploader KnowledgeFileUploader
}

// NewKnowledgeHandler creates a KnowledgeHandler.
func NewKnowledgeHandler(stats KnowledgeStats) *KnowledgeHandler {
	return &KnowledgeHandler{
		stats: stats,
	}
}

// SetFileLister sets the file lister (optional, may not be wired in all deployments).
func (h *KnowledgeHandler) SetFileLister(lister KnowledgeFileLister) {
	h.fileLister = lister
}

// knowledgeStatusResponse is the JSON response for GET .../knowledge/status.
type knowledgeStatusResponse struct {
	AgentName    string `json:"agent_name"`
	TotalFiles   int    `json:"total_files"`
	IndexedFiles int    `json:"indexed_files"`
	Status       string `json:"status"` // ready, indexing, empty
}

// Status handles GET /api/v1/agents/{name}/knowledge/status.
func (h *KnowledgeHandler) Status(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, "agent name is required")
		return
	}

	// Compute status from file list when available (accurate per-file status).
	if h.fileLister != nil {
		files, err := h.fileLister.ListFiles(r.Context(), name)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		totalFiles := len(files)
		indexedFiles := 0
		hasIndexing := false
		for _, f := range files {
			switch f.Status {
			case "ready":
				indexedFiles++
			case "indexing", "uploading":
				hasIndexing = true
			}
		}
		status := "empty"
		if totalFiles > 0 {
			if hasIndexing {
				status = "indexing"
			} else {
				status = "ready"
			}
		}
		writeJSON(w, http.StatusOK, knowledgeStatusResponse{
			AgentName:    name,
			TotalFiles:   totalFiles,
			IndexedFiles: indexedFiles,
			Status:       status,
		})
		return
	}

	// Fallback: use stats (no per-file granularity).
	docCount, _, _, err := h.stats.GetStats(r.Context(), name)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	status := "empty"
	if docCount > 0 {
		status = "ready"
	}
	writeJSON(w, http.StatusOK, knowledgeStatusResponse{
		AgentName:    name,
		TotalFiles:   docCount,
		IndexedFiles: docCount,
		Status:       status,
	})
}

// ListFiles handles GET /api/v1/agents/{name}/knowledge (AC-KB-LIST-01..02).
func (h *KnowledgeHandler) ListFiles(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, "agent name is required")
		return
	}

	if h.fileLister == nil {
		writeJSONError(w, http.StatusNotImplemented, "Knowledge indexing requires an embedding model. Configure one in Models → select type Embeddings.")
		return
	}

	files, err := h.fileLister.ListFiles(r.Context(), name)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	if files == nil {
		files = []KnowledgeFileResponse{}
	}

	writeJSON(w, http.StatusOK, files)
}

// DeleteFile handles DELETE /api/v1/agents/{name}/knowledge/{file_id} (AC-KB-LIST-04).
func (h *KnowledgeHandler) DeleteFile(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	fileID := chi.URLParam(r, "file_id")
	if name == "" || fileID == "" {
		writeJSONError(w, http.StatusBadRequest, "agent name and file_id are required")
		return
	}

	if h.fileLister == nil {
		writeJSONError(w, http.StatusNotImplemented, "Knowledge indexing requires an embedding model. Configure one in Models → select type Embeddings.")
		return
	}

	if err := h.fileLister.DeleteFile(r.Context(), name, fileID); err != nil {
		writeDomainError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- WP-3: File Upload ---

// KnowledgeFileUploader handles file upload, storage, and async indexing.
type KnowledgeFileUploader interface {
	UploadFile(ctx context.Context, tenantID, agentName, fileName, fileType string, fileSize int64, fileHash string, content []byte) (*KnowledgeFileResponse, error)
}

// SetFileUploader configures the file uploader (WP-3).
func (h *KnowledgeHandler) SetFileUploader(uploader KnowledgeFileUploader) {
	h.fileUploader = uploader
}

// maxUploadSize is the maximum file upload size (50 MB).
const maxUploadSize = 50 << 20

// allowedMIMETypes maps allowed file extensions to expected MIME prefixes.
var allowedMIMETypes = map[string][]string{
	".txt":  {"text/plain"},
	".md":   {"text/plain", "text/markdown", "application/octet-stream"},
	".csv":  {"text/csv", "text/plain", "application/csv", "application/octet-stream"},
	".pdf":  {"application/pdf", "application/octet-stream"},
	".docx": {"application/vnd.openxmlformats-officedocument.wordprocessingml.document", "application/octet-stream", "application/zip"},
}

// UploadFile handles POST /api/v1/agents/{name}/knowledge/files (WP-3).
func (h *KnowledgeHandler) UploadFile(w http.ResponseWriter, r *http.Request) {
	agentName := chi.URLParam(r, "name")
	if agentName == "" {
		writeJSONError(w, http.StatusBadRequest, "agent name is required")
		return
	}

	if h.fileUploader == nil {
		writeJSONError(w, http.StatusNotImplemented, "Knowledge indexing requires an embedding model. Configure one in Models → select type Embeddings.")
		return
	}

	// Limit request body size
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize+1024) // +1KB for multipart headers

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

	// Validate filename — sanitize path traversal
	originalName := sanitizeUploadFilename(header.Filename)
	if originalName == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid filename")
		return
	}

	// Validate file extension
	ext := strings.ToLower(filepath.Ext(originalName))
	allowedMIME, ok := allowedMIMETypes[ext]
	if !ok {
		writeJSONError(w, http.StatusBadRequest,
			fmt.Sprintf("unsupported file type %q, supported: txt, md, csv, pdf, docx", ext))
		return
	}

	// Validate MIME type
	if !validateMIME(header, allowedMIME) {
		writeJSONError(w, http.StatusBadRequest,
			fmt.Sprintf("file content type does not match extension %q", ext))
		return
	}

	// Read file content
	content, err := io.ReadAll(file)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "failed to read file")
		return
	}

	if len(content) == 0 {
		writeJSONError(w, http.StatusBadRequest, "empty file")
		return
	}

	// Compute hash
	fileHash := fmt.Sprintf("%x", sha256.Sum256(content))

	// Determine file type from extension
	fileType := strings.TrimPrefix(ext, ".")

	// Extract tenant from context (CE mode → CETenantID)
	tenantID := domain.TenantIDFromContext(r.Context())
	if tenantID == "" {
		tenantID = domain.CETenantID
	}

	resp, err := h.fileUploader.UploadFile(r.Context(), tenantID, agentName, originalName, fileType, int64(len(content)), fileHash, content)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, resp)
}

// sanitizeUploadFilename removes path traversal characters and returns the base filename.
func sanitizeUploadFilename(name string) string {
	// Use filepath.Base to strip directory components
	name = filepath.Base(name)
	// Remove any remaining path separators
	name = strings.ReplaceAll(name, "..", "")
	name = strings.ReplaceAll(name, "/", "")
	name = strings.ReplaceAll(name, "\\", "")
	name = strings.TrimSpace(name)
	if name == "." || name == "" {
		return ""
	}
	return name
}

// validateMIME checks the Content-Type header against allowed MIME types.
func validateMIME(header *multipart.FileHeader, allowed []string) bool {
	ct := header.Header.Get("Content-Type")
	if ct == "" {
		return true // browsers may not send Content-Type for some file types
	}
	ct = strings.ToLower(strings.Split(ct, ";")[0]) // strip charset
	for _, a := range allowed {
		if ct == a {
			return true
		}
	}
	return false
}
