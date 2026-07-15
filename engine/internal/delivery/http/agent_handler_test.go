package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockAgentLister struct {
	agents []AgentInfo
	detail *AgentDetail
	err    error
}

func (m *mockAgentLister) ListAgents(_ context.Context) ([]AgentInfo, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.agents, nil
}

func (m *mockAgentLister) GetAgent(_ context.Context, name string) (*AgentDetail, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.detail != nil && m.detail.Name == name {
		return m.detail, nil
	}
	return nil, nil
}

func newAgentRouter(handler *AgentHandler) http.Handler {
	return newAgentTestRouter(handler)
}

func TestAgentHandler_List(t *testing.T) {
	agents := []AgentInfo{
		{Name: "sales", Description: "Sales agent", ToolsCount: 5},
		{Name: "coder", ToolsCount: 3, HasKnowledge: true},
	}
	handler := NewAgentHandler(&mockAgentLister{agents: agents})
	router := newAgentRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var result []AgentInfo
	err := json.NewDecoder(rec.Body).Decode(&result)
	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, "sales", result[0].Name)
	assert.Equal(t, "coder", result[1].Name)
}

func TestAgentHandler_List_Empty(t *testing.T) {
	handler := NewAgentHandler(&mockAgentLister{agents: []AgentInfo{}})
	router := newAgentRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var result []AgentInfo
	err := json.NewDecoder(rec.Body).Decode(&result)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestAgentHandler_List_Error(t *testing.T) {
	handler := NewAgentHandler(&mockAgentLister{err: fmt.Errorf("db error")})
	router := newAgentRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestAgentHandler_Get(t *testing.T) {
	detail := &AgentDetail{
		AgentInfo: AgentInfo{Name: "sales", Description: "Sales agent", ToolsCount: 2},
		Tools:     []string{"search", "email"},
		CanSpawn:  []string{"researcher"},
	}
	handler := NewAgentHandler(&mockAgentLister{detail: detail})
	router := newAgentRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/sales", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var result AgentDetail
	err := json.NewDecoder(rec.Body).Decode(&result)
	require.NoError(t, err)
	assert.Equal(t, "sales", result.Name)
	assert.Equal(t, []string{"search", "email"}, result.Tools)
}

func TestAgentHandler_Get_NotFound(t *testing.T) {
	handler := NewAgentHandler(&mockAgentLister{})
	router := newAgentRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/nonexistent", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)

	var result map[string]string
	err := json.NewDecoder(rec.Body).Decode(&result)
	require.NoError(t, err)
	assert.Contains(t, result["error"], "nonexistent")
}

// TestAgentHandler_CanSpawnRejected pins the honest-API contract: the agent
// upsert endpoints never persisted can_spawn (delegation targets live in
// agent_relations), so a non-empty can_spawn is rejected with a pointer to
// the relations API instead of being silently dropped.
func TestAgentHandler_CanSpawnRejected(t *testing.T) {
	reached := false
	mgr := &mockAgentManager{
		createFunc: func(_ context.Context, req CreateAgentRequest) (*AgentDetail, error) {
			reached = true
			return &AgentDetail{AgentInfo: AgentInfo{Name: req.Name}}, nil
		},
		updateFunc: func(_ context.Context, name string, _ CreateAgentRequest) (*AgentDetail, error) {
			reached = true
			return &AgentDetail{AgentInfo: AgentInfo{Name: name}}, nil
		},
		patchFunc: func(_ context.Context, name string, _ UpdateAgentRequest) (*AgentDetail, error) {
			reached = true
			return &AgentDetail{AgentInfo: AgentInfo{Name: name}}, nil
		},
	}
	router := newAgentManagerRouter(mgr)

	tests := []struct {
		method, url, body string
	}{
		{http.MethodPost, "/api/v1/agents", `{"name":"a1","system_prompt":"p","can_spawn":["child"]}`},
		{http.MethodPut, "/api/v1/agents/a1", `{"system_prompt":"p","can_spawn":["child"]}`},
		{http.MethodPatch, "/api/v1/agents/a1", `{"can_spawn":["child"]}`},
	}
	for _, tc := range tests {
		req := httptest.NewRequest(tc.method, tc.url, strings.NewReader(tc.body))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equalf(t, http.StatusBadRequest, rec.Code,
			"%s %s with non-empty can_spawn must be 400, got %d: %s", tc.method, tc.url, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), "agent-relations",
			"rejection must point the caller to the relations API")
	}
	require.False(t, reached, "manager must not be reached when can_spawn is rejected")

	// Empty can_spawn stays a no-op (the admin SPA sends can_spawn: [] on
	// every save until its editor ships read-only) — the write goes through.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents",
		strings.NewReader(`{"name":"a1","system_prompt":"p","can_spawn":[]}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.True(t, reached, "empty can_spawn must pass through to the manager")
}
