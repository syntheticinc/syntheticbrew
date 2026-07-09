package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockTokenRepository struct {
	tokens    []TokenInfo
	nextID    int
	createErr error
	deleteErr error
}

func newMockTokenRepository() *mockTokenRepository {
	return &mockTokenRepository{nextID: 1}
}

func (m *mockTokenRepository) Create(_ context.Context, _, name, tokenHash string, scopesMask int) (string, error) {
	if m.createErr != nil {
		return "", m.createErr
	}
	id := fmt.Sprintf("%d", m.nextID)
	m.nextID++
	m.tokens = append(m.tokens, TokenInfo{
		ID:         id,
		Name:       name,
		ScopesMask: scopesMask,
		CreatedAt:  time.Now(),
	})
	return id, nil
}

func (m *mockTokenRepository) List(_ context.Context) ([]TokenInfo, error) {
	return m.tokens, nil
}

func (m *mockTokenRepository) Delete(_ context.Context, id string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	for i, t := range m.tokens {
		if t.ID == id {
			m.tokens = append(m.tokens[:i], m.tokens[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("token not found")
}

func TestTokenHandler_CreateToken(t *testing.T) {
	repo := newMockTokenRepository()
	h := NewTokenHandler(repo)

	body := `{"name":"my-token","scopes_mask":3}`
	req := httptest.NewRequest(http.MethodPost, "/auth/tokens", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.CreateToken(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)

	var resp createTokenResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "1", resp.ID)
	assert.Equal(t, "my-token", resp.Name)
	assert.True(t, strings.HasPrefix(resp.Token, "bb_"))
	assert.Len(t, resp.Token, 3+64) // "bb_" + 32 bytes hex

	// Verify token stored in repo
	assert.Len(t, repo.tokens, 1)
	assert.Equal(t, "my-token", repo.tokens[0].Name)
	assert.Equal(t, 3, repo.tokens[0].ScopesMask)
}

func TestTokenHandler_CreateToken_EmptyName(t *testing.T) {
	repo := newMockTokenRepository()
	h := NewTokenHandler(repo)

	body := `{"name":"","scopes_mask":1}`
	req := httptest.NewRequest(http.MethodPost, "/auth/tokens", strings.NewReader(body))
	rec := httptest.NewRecorder()

	h.CreateToken(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "name required")
}

func TestTokenHandler_CreateToken_ColonInNameRejected(t *testing.T) {
	repo := newMockTokenRepository()
	h := NewTokenHandler(repo)

	body := `{"name":"support:alice","scopes_mask":1}`
	req := httptest.NewRequest(http.MethodPost, "/auth/tokens", strings.NewReader(body))
	rec := httptest.NewRecorder()

	h.CreateToken(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "must not contain ':'")
}

func TestTokenHandler_CreateToken_DuplicateName(t *testing.T) {
	repo := newMockTokenRepository()
	repo.createErr = fmt.Errorf("duplicate name")
	h := NewTokenHandler(repo)

	body := `{"name":"dup","scopes_mask":1}`
	req := httptest.NewRequest(http.MethodPost, "/auth/tokens", strings.NewReader(body))
	rec := httptest.NewRecorder()

	h.CreateToken(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestTokenHandler_ListTokens(t *testing.T) {
	repo := newMockTokenRepository()
	repo.tokens = []TokenInfo{
		{ID: "1", Name: "token-1", ScopesMask: 1, CreatedAt: time.Now()},
		{ID: "2", Name: "token-2", ScopesMask: 3, CreatedAt: time.Now()},
	}
	h := NewTokenHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/auth/tokens", nil)
	rec := httptest.NewRecorder()

	h.ListTokens(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var tokens []TokenInfo
	err := json.NewDecoder(rec.Body).Decode(&tokens)
	require.NoError(t, err)
	assert.Len(t, tokens, 2)
	assert.Equal(t, "token-1", tokens[0].Name)
	assert.Equal(t, "token-2", tokens[1].Name)
}

func TestTokenHandler_DeleteToken(t *testing.T) {
	repo := newMockTokenRepository()
	repo.tokens = []TokenInfo{
		{ID: "11111111-1111-1111-1111-111111111111", Name: "to-delete", ScopesMask: 1, CreatedAt: time.Now()},
	}
	h := NewTokenHandler(repo)

	// Use chi router to extract URL param
	r := chi.NewRouter()
	r.Delete("/auth/tokens/{id}", h.DeleteToken)

	req := httptest.NewRequest(http.MethodDelete, "/auth/tokens/11111111-1111-1111-1111-111111111111", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Len(t, repo.tokens, 0)
}

func TestTokenHandler_DeleteToken_NotFound(t *testing.T) {
	repo := newMockTokenRepository()
	h := NewTokenHandler(repo)

	r := chi.NewRouter()
	r.Delete("/auth/tokens/{id}", h.DeleteToken)

	req := httptest.NewRequest(http.MethodDelete, "/auth/tokens/99999999-9999-9999-9999-999999999999", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestTokenHandler_CreateToken_ScopeNames verifies Bug 3 fix: symbolic
// scope names ({"scopes":["api"]}) are translated into the right bitmask
// and persisted. Without this the token had scopes_mask=0 and 403'd every
// subsequent call — authenticated but forbidden, indistinguishable from a
// broken token.
func TestTokenHandler_CreateToken_ScopeNames(t *testing.T) {
	tests := []struct {
		name       string
		scopes     []string
		wantMask   int
		wantStatus int
	}{
		{
			name:       "api expands into integration scopes",
			scopes:     []string{"api"},
			wantMask:   ScopeAPIMask,
			wantStatus: http.StatusCreated,
		},
		{
			name:       "chat only",
			scopes:     []string{"chat"},
			wantMask:   ScopeChat,
			wantStatus: http.StatusCreated,
		},
		{
			name:       "union: chat + tasks",
			scopes:     []string{"chat", "tasks"},
			wantMask:   ScopeChat | ScopeTasks,
			wantStatus: http.StatusCreated,
		},
		{
			name:       "api does NOT include agents:write",
			scopes:     []string{"api"},
			wantMask:   ScopeAPIMask,
			wantStatus: http.StatusCreated,
		},
		{
			name:       "api does NOT include admin",
			scopes:     []string{"api"},
			wantMask:   ScopeAPIMask,
			wantStatus: http.StatusCreated,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newMockTokenRepository()
			h := NewTokenHandler(repo)

			// Build the body as JSON to exercise the real decoder.
			body := `{"name":"integration","scopes":` + jsonMustMarshalStrings(tt.scopes) + `}`
			req := httptest.NewRequest(http.MethodPost, "/auth/tokens", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			h.CreateToken(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
			if tt.wantStatus == http.StatusCreated {
				require.Len(t, repo.tokens, 1)
				assert.Equal(t, tt.wantMask, repo.tokens[0].ScopesMask)
				// api must not grant destructive admin bits.
				if contains(tt.scopes, "api") {
					assert.Zero(t, repo.tokens[0].ScopesMask&ScopeAgentsWrite,
						"api scope must not grant agents:write")
					assert.Zero(t, repo.tokens[0].ScopesMask&ScopeSchemasWrite,
						"api scope must not grant schemas:write")
					assert.Zero(t, repo.tokens[0].ScopesMask&ScopeAdmin,
						"api scope must not grant admin")
					assert.Zero(t, repo.tokens[0].ScopesMask&ScopeConfig,
						"api scope must not grant config")
					assert.Zero(t, repo.tokens[0].ScopesMask&ScopeModelsWrite,
						"api scope must not grant models:write")
					assert.Zero(t, repo.tokens[0].ScopesMask&ScopeMCPWrite,
						"api scope must not grant mcp:write")
					// But must grant the expected read bits.
					assert.NotZero(t, repo.tokens[0].ScopesMask&ScopeChat)
					assert.NotZero(t, repo.tokens[0].ScopesMask&ScopeTasks)
					assert.NotZero(t, repo.tokens[0].ScopesMask&ScopeAgentsRead)
					assert.NotZero(t, repo.tokens[0].ScopesMask&ScopeSchemasRead)
				}
			}
		})
	}
}

// TestTokenHandler_CreateToken_ZeroMaskRejected verifies we no longer accept
// the pathological scopes_mask=0 (Bug 3): used to silently authenticate a
// token that 403'd every request.
func TestTokenHandler_CreateToken_ZeroMaskRejected(t *testing.T) {
	repo := newMockTokenRepository()
	h := NewTokenHandler(repo)

	body := `{"name":"empty-scopes","scopes_mask":0}`
	req := httptest.NewRequest(http.MethodPost, "/auth/tokens", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.CreateToken(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "scopes required")
	assert.Len(t, repo.tokens, 0, "zero-mask token must not be persisted")
}

// TestTokenHandler_CreateToken_UnknownScopeNamesFall Through verifies that a
// list containing only unknown names yields mask=0 and is rejected, not
// silently allowed.
func TestTokenHandler_CreateToken_UnknownScopeNamesRejected(t *testing.T) {
	repo := newMockTokenRepository()
	h := NewTokenHandler(repo)

	body := `{"name":"typos","scopes":["xyzzy","does-not-exist"]}`
	req := httptest.NewRequest(http.MethodPost, "/auth/tokens", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.CreateToken(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestTokenHandler_CreateToken_MaskAndScopesUnion verifies that a caller
// supplying both forms gets the OR of the two — some callers migrate
// gradually and may mix names with legacy numeric masks.
func TestTokenHandler_CreateToken_MaskAndScopesUnion(t *testing.T) {
	repo := newMockTokenRepository()
	h := NewTokenHandler(repo)

	body := `{"name":"mixed","scopes_mask":1,"scopes":["tasks"]}`
	req := httptest.NewRequest(http.MethodPost, "/auth/tokens", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.CreateToken(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	require.Len(t, repo.tokens, 1)
	assert.Equal(t, ScopeChat|ScopeTasks, repo.tokens[0].ScopesMask)
}

// TestScopesToMask is a focused test on the pure mapping — used both by
// token creation and by any future callers that need to translate admin
// UI scope names into a mask.
func TestScopesToMask(t *testing.T) {
	tests := []struct {
		name   string
		scopes []string
		want   int
	}{
		{"empty", nil, 0},
		{"unknown only", []string{"xyz"}, 0},
		{"single chat", []string{"chat"}, ScopeChat},
		{"api", []string{"api"}, ScopeAPIMask},
		{"alias agents", []string{"agents"}, ScopeAgentsRead},
		{"agents:read explicit", []string{"agents:read"}, ScopeAgentsRead},
		{"agents:write", []string{"agents:write"}, ScopeAgentsWrite},
		{"duplicate names are idempotent", []string{"chat", "chat"}, ScopeChat},
		{"union", []string{"chat", "tasks", "agents:read"}, ScopeChat | ScopeTasks | ScopeAgentsRead},
		{"known + unknown = known bits only", []string{"chat", "xyz"}, ScopeChat},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ScopesToMask(tt.scopes))
		})
	}
}

// helpers (test-only)

func jsonMustMarshalStrings(ss []string) string {
	// Tiny helper: encode []string as a JSON array. Keeps the inline body
	// literals compact and readable at the call site.
	if ss == nil {
		return "null"
	}
	b := `[`
	for i, s := range ss {
		if i > 0 {
			b += ","
		}
		b += `"` + s + `"`
	}
	b += `]`
	return b
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
