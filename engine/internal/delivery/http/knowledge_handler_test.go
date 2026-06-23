package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
)

type mockKnowledgeStats struct {
	docs    int
	chunks  int
	lastIdx *time.Time
	err     error
}

func (m *mockKnowledgeStats) GetStats(_ context.Context, _ string) (int, int, *time.Time, error) {
	return m.docs, m.chunks, m.lastIdx, m.err
}

func newKnowledgeRouter(handler *KnowledgeHandler) *chi.Mux {
	r := chi.NewRouter()
	r.Get("/api/v1/agents/{name}/knowledge/status", handler.Status)
	return r
}

func TestKnowledgeHandler_Status(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	stats := &mockKnowledgeStats{docs: 5, chunks: 42, lastIdx: &now}
	handler := NewKnowledgeHandler(stats)
	router := newKnowledgeRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/sales/knowledge/status", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, `"agent_name":"sales"`)
	assert.Contains(t, body, `"total_files":5`)
	assert.Contains(t, body, `"status":"ready"`)
}

func TestKnowledgeHandler_Status_NoDocuments(t *testing.T) {
	stats := &mockKnowledgeStats{docs: 0, chunks: 0, lastIdx: nil}
	handler := NewKnowledgeHandler(stats)
	router := newKnowledgeRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/empty-agent/knowledge/status", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"total_files":0`)
}
