package react

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/llm"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

func TestBuildChatCallOptions_SessionIDAndModifier(t *testing.T) {
	mod := func(b []byte) ([]byte, error) { return b, nil }

	// Both present → 2 options (modifier + header).
	assert.Len(t, buildChatCallOptions(mod, "sess-1"), 2)
	// Only session id → 1 option (header).
	assert.Len(t, buildChatCallOptions(nil, "sess-1"), 1)
	// Only modifier → 1 option.
	assert.Len(t, buildChatCallOptions(mod, ""), 1)
	// Neither → none.
	assert.Empty(t, buildChatCallOptions(nil, ""))
	// Unsafe (header-injection-shaped) session id → header skipped, modifier kept.
	assert.Len(t, buildChatCallOptions(mod, "bad\r\nX: y"), 1)
}

func TestHeaderSafeSessionID(t *testing.T) {
	assert.True(t, headerSafeSessionID("550e8400-e29b-41d4-a716-446655440000"))
	assert.True(t, headerSafeSessionID("conversation-abc_123"))
	assert.False(t, headerSafeSessionID(""), "empty → no header")
	assert.False(t, headerSafeSessionID("a\r\nInjected: 1"), "CRLF → unsafe")
	assert.False(t, headerSafeSessionID("a\tb"), "tab/control → unsafe")
	assert.False(t, headerSafeSessionID("café-session"), "non-ASCII → unsafe")
	assert.False(t, headerSafeSessionID(strings.Repeat("x", 257)), ">256 chars → unsafe")
}

// TestOwnedGraph_SessionIDHeaderReachesWire proves the agent's session id leaves
// the owned-graph chat node as an x-session-id header — the lever that makes
// OpenRouter sticky-route to one provider and keep its prefix cache warm.
func TestOwnedGraph_SessionIDHeaderReachesWire(t *testing.T) {
	var gotSession string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSession = r.Header.Get("x-session-id")
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c","object":"chat.completion","created":1,"model":"m",
			"choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}`))
	}))
	defer srv.Close()

	client, err := llm.CreateClientFromDBModel(models.LLMProviderModel{
		Type: "openai_compatible", BaseURL: srv.URL, ModelName: "m", APIKeyEncrypted: "k",
	}, nil)
	require.NoError(t, err)

	const sessionID = "conversation-abc-123"
	agent, err := NewAgent(context.Background(), AgentConfig{
		ChatModel:    client,
		MaxSteps:     4,
		SessionID:    sessionID,
		AgentConfig:  charAgentConfig(nil),
		ModelName:    "m",
		ProviderType: "openai_compatible",
		// No cache modifier — isolate the session-id header path.
	})
	require.NoError(t, err)

	_, err = agent.RunWithCallbacks(context.Background(), "do it",
		func(*domain.AgentEvent) error { return nil })
	require.NoError(t, err)

	assert.Equal(t, sessionID, gotSession,
		"the agent's session id must leave the chat node as x-session-id (enables OpenRouter sticky routing)")
}
