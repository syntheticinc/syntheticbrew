package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
