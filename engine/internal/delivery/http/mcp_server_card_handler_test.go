package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMCPServerCard_AnonymousRequest_ServesValidJSON verifies the server card
// is served to a request carrying no auth context at all (the whole point of
// the endpoint: a catalog-scanner fallback that never passes OAuth).
func TestMCPServerCard_AnonymousRequest_ServesValidJSON(t *testing.T) {
	h := NewMCPServerCardHandler("1.2.3", "https://engine.example.com/api/v1/mcp/rpc")

	req := httptest.NewRequest(http.MethodGet, "/.well-known/mcp/server-card.json", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var card mcpServerCard
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &card))
	assert.Equal(t, "syntheticbrew-engine", card.Name)
	assert.Equal(t, mcpServerTitle, card.Title)
	assert.NotEmpty(t, card.Description)
	assert.Equal(t, "1.2.3", card.Version)
	assert.Equal(t, "https://engine.example.com/api/v1/mcp/rpc", card.Endpoint)
	assert.Equal(t, "streamable-http", card.Transport)
	assert.Contains(t, card.Capabilities, "tools")
	assert.NotEmpty(t, card.Instructions)
}

// TestMCPServerCard_RelativeEndpointFallback verifies the card degrades to the
// endpoint path when no public base URL is configured.
func TestMCPServerCard_RelativeEndpointFallback(t *testing.T) {
	h := NewMCPServerCardHandler("dev", "/api/v1/mcp/rpc")

	req := httptest.NewRequest(http.MethodGet, "/.well-known/mcp/server-card.json", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var card mcpServerCard
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &card))
	assert.Equal(t, "/api/v1/mcp/rpc", card.Endpoint)
}
