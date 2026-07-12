//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestLinkKBToMissingAgentReturns404 guards a fixed defect: linking a knowledge
// base to a nonexistent agent returned HTTP 500 (an untyped "agent not found"
// error) while the missing-KB path already returned 404. Bad input on an admin
// API must surface as a 4xx, never a 5xx (SCC-03), and must not leak internal
// error tokens.
func TestLinkKBToMissingAgentReturns404(t *testing.T) {
	embID := seedEmbeddingModelForKB(t, "kb-link-404-embed")
	kb := createKBWithEmbedding(t, "kb-link-404", embID)

	resp := do(t, http.MethodPost,
		"/api/v1/knowledge-bases/"+kb.Name+"/agents/no-such-agent-xyz", nil, adminToken)
	body := readBody(t, resp)

	assert.Equalf(t, http.StatusNotFound, resp.StatusCode,
		"link to a missing agent must be 404, not 500: %s", body)

	low := strings.ToLower(string(body))
	for _, tok := range []string{"sqlstate", "constraint", "chk_", "idx_", "fk_", "pq:"} {
		assert.NotContainsf(t, low, tok, "response must not leak internal token %q: %s", tok, body)
	}
}
