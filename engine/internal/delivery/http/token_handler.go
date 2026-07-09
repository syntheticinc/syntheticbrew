package http

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/syntheticinc/syntheticbrew/internal/authprim"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// TokenRepository manages API tokens in the database.
type TokenRepository interface {
	Create(ctx context.Context, userSub, name, tokenHash string, scopesMask int) (id string, err error)
	List(ctx context.Context) ([]TokenInfo, error)
	Delete(ctx context.Context, id string) error
}

// TokenInfo is a token record returned by List (no raw token value).
type TokenInfo struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	ScopesMask int        `json:"scopes_mask"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// TokenHandler handles API token CRUD endpoints.
type TokenHandler struct {
	repo TokenRepository
}

// NewTokenHandler creates a new TokenHandler.
func NewTokenHandler(repo TokenRepository) *TokenHandler {
	return &TokenHandler{repo: repo}
}

// createTokenRequest is the body for POST /auth/tokens.
//
// Callers may supply either form:
//   - `scopes_mask` (int) — raw bitmask, the legacy numeric form.
//   - `scopes`      ([]string) — symbolic names ("api", "chat",
//     "agents:read", ...). Names are mapped via ScopeNameToMask; the
//     union of all recognised names is stored.
//
// When both are present they are OR-ed. When neither is present (or the
// names list resolves to mask=0) the token is rejected with 400 — Bug 3
// regression: we used to happily accept scopes_mask=0 and then 403 every
// subsequent call, which is indistinguishable from a broken token.
type createTokenRequest struct {
	Name       string   `json:"name"`
	ScopesMask int      `json:"scopes_mask,omitempty"`
	Scopes     []string `json:"scopes,omitempty"`
}

type createTokenResponse struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Token      string `json:"token"`
	ScopesMask int    `json:"scopes_mask"`
}

// CreateToken handles POST /auth/tokens.
func (h *TokenHandler) CreateToken(w http.ResponseWriter, r *http.Request) {
	var req createTokenRequest
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if req.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
		return
	}
	// The token name becomes the caller's identity, and for widget traffic a
	// per-visitor id is appended as "<name>:<visitor>". Forbid ':' in the name
	// so a name can never be crafted to collide with another principal's
	// namespaced identity.
	if strings.Contains(req.Name, ":") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name must not contain ':'"})
		return
	}

	// Bug 3: resolve the final bitmask from both `scopes_mask` and the
	// symbolic `scopes` list. A token with mask=0 would authenticate but
	// 403 every request — reject at create-time instead.
	mask := req.ScopesMask | ScopesToMask(req.Scopes)
	if mask == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "scopes required: supply non-zero scopes_mask or scopes like [\"api\"]",
		})
		return
	}

	rawToken, err := authprim.Generate()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "generate token failed"})
		return
	}

	userSub := domain.UserSubFromContext(r.Context())
	hash := authprim.Hash(rawToken)
	id, err := h.repo.Create(r.Context(), userSub, req.Name, hash, mask)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": fmt.Sprintf("create token: %s", err)})
		return
	}

	writeJSON(w, http.StatusCreated, createTokenResponse{
		ID:         id,
		Name:       req.Name,
		Token:      rawToken,
		ScopesMask: mask,
	})
}

// ListTokens handles GET /auth/tokens.
func (h *TokenHandler) ListTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := h.repo.List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("list tokens: %s", err)})
		return
	}

	writeJSON(w, http.StatusOK, tokens)
}

// DeleteToken handles DELETE /auth/tokens/{id}.
func (h *TokenHandler) DeleteToken(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid token id: must be a UUID"})
		return
	}

	if err := h.repo.Delete(r.Context(), id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("delete token: %s", err)})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
