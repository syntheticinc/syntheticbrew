package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/bytebrew/engine/internal/domain"
)

// authedRequest builds an httptest.Request with the auth ctx values that the
// session handler ACL pre-check expects: actor type "admin", ScopeAdmin scope
// bit, and a populated user_sub. Without this, extractSessionACL (added in
// 1.1.4 ownership hardening) sees an empty actor and the GetSession path
// short-circuits to 404 even when the mock service has the row.
func authedRequest(method, path string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, path, body)
	ctx := req.Context()
	ctx = context.WithValue(ctx, ContextKeyActorType, "admin")
	ctx = context.WithValue(ctx, ContextKeyScopes, ScopeAdmin)
	ctx = domain.WithUserSub(ctx, "admin-test")
	return req.WithContext(ctx)
}

type mockSessionService struct {
	sessions []SessionResponse
	total    int64
	session  *SessionResponse
	created  *SessionResponse
	updated  *SessionResponse
	err      error

	lastListAgentName string
	lastListUserSub   string
	lastListStatus    string
	lastListFrom      string
	lastListTo        string
	lastListPage      int
	lastListPerPage   int
	lastDeleteID      string
}

func (m *mockSessionService) ListSessions(_ context.Context, agentName, userSub, status, from, to string, page, perPage int) ([]SessionResponse, int64, error) {
	m.lastListAgentName = agentName
	m.lastListUserSub = userSub
	m.lastListStatus = status
	m.lastListFrom = from
	m.lastListTo = to
	m.lastListPage = page
	m.lastListPerPage = perPage
	if m.err != nil {
		return nil, 0, m.err
	}
	return m.sessions, m.total, nil
}

func (m *mockSessionService) GetSession(_ context.Context, id string) (*SessionResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.session != nil && m.session.ID == id {
		return m.session, nil
	}
	return nil, nil
}

func (m *mockSessionService) CreateSession(_ context.Context, _ CreateSessionRequest) (*SessionResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.created, nil
}

func (m *mockSessionService) UpdateSession(_ context.Context, id string, _ UpdateSessionRequest) (*SessionResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.updated != nil {
		return m.updated, nil
	}
	return nil, nil
}

func (m *mockSessionService) DeleteSession(_ context.Context, id string) error {
	m.lastDeleteID = id
	return m.err
}

func newSessionRouter(handler *SessionHandler) http.Handler {
	return newSessionTestRouter(handler)
}

func TestSessionHandler_List(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		sessions   []SessionResponse
		total      int64
		wantStatus int
		wantTotal  int64
	}{
		{
			name:  "returns paginated sessions",
			query: "?page=1&per_page=10",
			sessions: []SessionResponse{
				{ID: "s1", UserSub: "u1", Status: "active", CreatedAt: "2026-03-19T10:00:00Z", UpdatedAt: "2026-03-19T10:05:00Z"},
			},
			total:      1,
			wantStatus: http.StatusOK,
			wantTotal:  1,
		},
		{
			name:       "empty list",
			query:      "",
			sessions:   []SessionResponse{},
			total:      0,
			wantStatus: http.StatusOK,
			wantTotal:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &mockSessionService{sessions: tt.sessions, total: tt.total}
			handler := NewSessionHandler(svc)
			router := newSessionRouter(handler)

			req := authedRequest(http.MethodGet, "/api/v1/sessions"+tt.query, nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)

			var resp PaginatedSessionResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
			assert.Equal(t, tt.wantTotal, resp.Total)
			assert.Equal(t, len(tt.sessions), len(resp.Data))
		})
	}
}

func TestSessionHandler_List_Filters(t *testing.T) {
	svc := &mockSessionService{sessions: []SessionResponse{}, total: 0}
	handler := NewSessionHandler(svc)
	router := newSessionRouter(handler)

	req := authedRequest(http.MethodGet, "/api/v1/sessions?agent_name=sales&user_sub=u1&status=active&page=2&per_page=5", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "sales", svc.lastListAgentName)
	assert.Equal(t, "u1", svc.lastListUserSub)
	assert.Equal(t, "active", svc.lastListStatus)
	assert.Equal(t, 2, svc.lastListPage)
	assert.Equal(t, 5, svc.lastListPerPage)
}

func TestSessionHandler_Get(t *testing.T) {
	tests := []struct {
		name       string
		id         string
		session    *SessionResponse
		wantStatus int
	}{
		{
			name:       "found",
			id:         "11111111-1111-1111-1111-111111111111",
			session:    &SessionResponse{ID: "11111111-1111-1111-1111-111111111111", UserSub: "u1", Status: "active", CreatedAt: "2026-03-19T10:00:00Z", UpdatedAt: "2026-03-19T10:05:00Z"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "not found",
			id:         "99999999-9999-9999-9999-999999999999",
			session:    &SessionResponse{ID: "11111111-1111-1111-1111-111111111111"},
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &mockSessionService{session: tt.session}
			handler := NewSessionHandler(svc)
			router := newSessionRouter(handler)

			req := authedRequest(http.MethodGet, "/api/v1/sessions/"+tt.id, nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}

func TestSessionHandler_Create(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		created    *SessionResponse
		wantStatus int
	}{
		{
			name:       "valid request",
			body:       `{"user_sub":"u1","title":"Help me"}`,
			created:    &SessionResponse{ID: "s1", UserSub: "u1", Title: "Help me", Status: "active", CreatedAt: "2026-03-19T10:00:00Z", UpdatedAt: "2026-03-19T10:00:00Z"},
			wantStatus: http.StatusCreated,
		},
		{
			name:       "invalid json",
			body:       `{invalid}`,
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &mockSessionService{created: tt.created}
			handler := NewSessionHandler(svc)
			router := newSessionRouter(handler)

			req := authedRequest(http.MethodPost, "/api/v1/sessions", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
			if tt.wantStatus == http.StatusCreated {
				var resp SessionResponse
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
				assert.Equal(t, tt.created.ID, resp.ID)
				assert.Equal(t, tt.created.Title, resp.Title)
			}
		})
	}
}

func TestSessionHandler_Update(t *testing.T) {
	const id = "11111111-1111-1111-1111-111111111111"
	existing := &SessionResponse{ID: id, UserSub: "admin-test", Status: "active", CreatedAt: "2026-03-19T10:00:00Z", UpdatedAt: "2026-03-19T10:05:00Z"}
	updated := &SessionResponse{ID: id, UserSub: "admin-test", Title: "New title", Status: "active", CreatedAt: "2026-03-19T10:00:00Z", UpdatedAt: "2026-03-19T10:06:00Z"}
	svc := &mockSessionService{session: existing, updated: updated}
	handler := NewSessionHandler(svc)
	router := newSessionRouter(handler)

	body := `{"title":"New title"}`
	req := authedRequest(http.MethodPut, "/api/v1/sessions/"+id, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp SessionResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "New title", resp.Title)
}

func TestSessionHandler_Update_NotFound(t *testing.T) {
	svc := &mockSessionService{} // GetSession returns nil → 404 from ACL pre-check
	handler := NewSessionHandler(svc)
	router := newSessionRouter(handler)

	body := `{"title":"New title"}`
	req := authedRequest(http.MethodPut, "/api/v1/sessions/99999999-9999-9999-9999-999999999999", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestSessionHandler_Delete(t *testing.T) {
	const id = "11111111-1111-1111-1111-111111111111"
	existing := &SessionResponse{ID: id, UserSub: "admin-test", Status: "active"}
	svc := &mockSessionService{session: existing}
	handler := NewSessionHandler(svc)
	router := newSessionRouter(handler)

	req := authedRequest(http.MethodDelete, "/api/v1/sessions/"+id, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, id, svc.lastDeleteID)
}

func TestSessionHandler_Delete_Error(t *testing.T) {
	svc := &mockSessionService{err: fmt.Errorf("session not found: s999")}
	handler := NewSessionHandler(svc)
	router := newSessionRouter(handler)

	req := authedRequest(http.MethodDelete, "/api/v1/sessions/99999999-9999-9999-9999-999999999999", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestSessionHandler_List_PerPageCap(t *testing.T) {
	svc := &mockSessionService{sessions: []SessionResponse{}, total: 0}
	handler := NewSessionHandler(svc)
	router := newSessionRouter(handler)

	req := authedRequest(http.MethodGet, "/api/v1/sessions?per_page=200", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 100, svc.lastListPerPage) // capped at 100
}
