package llm

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// bigText returns a string long enough to clear the min-prefix char gate.
func bigText(prefix string) string {
	return prefix + ": " + strings.Repeat("lorem ipsum dolor sit amet ", 200)
}

// parseMessages extracts the messages array from a modified body for assertions.
func parseMessages(t *testing.T, body []byte) []map[string]json.RawMessage {
	t.Helper()
	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &top))
	var msgs []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(top["messages"], &msgs))
	return msgs
}

// lastPartHasCacheControl reports whether a message's content is an array whose
// last part carries cache_control:{type:ephemeral}.
func lastPartHasCacheControl(t *testing.T, msg map[string]json.RawMessage) bool {
	t.Helper()
	content, ok := msg["content"]
	if !ok || jsonKind(content) != jsonArray {
		return false
	}
	var parts []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(content, &parts))
	if len(parts) == 0 {
		return false
	}
	cc, ok := parts[len(parts)-1]["cache_control"]
	return ok && strings.Contains(string(cc), "ephemeral")
}

func TestNewCacheControlModifier_DefaultOnAndOptOut(t *testing.T) {
	enabled := &models.CacheControl{Enabled: true}
	disabled := &models.CacheControl{Enabled: false}

	cases := []struct {
		name     string
		provider string
		cc       *models.CacheControl
		wantNil  bool
	}{
		// Default-on: absent config on a honoring provider now caches.
		{"nil config honoring openai_compatible", "openai_compatible", nil, false},
		{"nil config honoring anthropic", "anthropic", nil, false},
		// Explicit opt-out on a honoring provider stays off.
		{"disabled opt-out openai_compatible", "openai_compatible", disabled, true},
		{"disabled opt-out anthropic", "anthropic", disabled, true},
		// Explicit enable on a honoring provider is on.
		{"openrouter explicit", "openai_compatible", enabled, false},
		{"anthropic explicit", "anthropic", enabled, false},
		// Non-honoring providers are always nil, even with nil or enabled config.
		{"openai automatic nil", "openai", nil, true},
		{"openai automatic enabled", "openai", enabled, true},
		{"azure automatic", "azure_openai", enabled, true},
		{"google automatic", "google", enabled, true},
		{"ollama local", "ollama", enabled, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mod := NewCacheControlModifier(tc.provider, tc.cc)
			if tc.wantNil {
				assert.Nil(t, mod, "modifier must be nil → not attached → byte-identical request")
				return
			}
			assert.NotNil(t, mod)
		})
	}
}

// TestCacheModifier_DefaultOnMarksLargeBodyAndNoOpsBelowGate exercises the
// default-on path (nil config) end-to-end: a large body gets a cache_control
// marker, and a body below the min-prefix gate passes through byte-identical.
func TestCacheModifier_DefaultOnMarksLargeBodyAndNoOpsBelowGate(t *testing.T) {
	mod := NewCacheControlModifier("openai_compatible", nil)
	require.NotNil(t, mod, "nil config on a honoring provider must default to caching on")

	bigBody := []byte(`{
		"messages": [
			{"role":"system","content":"` + bigText("you are helpful") + `"},
			{"role":"user","content":"` + bigText("the stable question") + `"}
		]
	}`)
	out, err := mod(bigBody)
	require.NoError(t, err)
	assert.Contains(t, string(out), "cache_control",
		"default-on modifier must mark a large body above the default min-prefix gate")

	// Below the default min-prefix gate → byte-identical no-op.
	smallBody := []byte(`{"messages":[{"role":"system","content":"tiny"},{"role":"user","content":"hi"}]}`)
	smallOut, err := mod(smallBody)
	require.NoError(t, err)
	assert.Equal(t, string(smallBody), string(smallOut),
		"a request below the min-prefix gate must stay byte-identical")
	assert.NotContains(t, string(smallOut), "cache_control")
}

func TestCacheModifier_InjectsOnStringContentPrefix(t *testing.T) {
	mod := NewCacheControlModifier("openai_compatible", &models.CacheControl{Enabled: true, MinPrefixTokens: 1})
	require.NotNil(t, mod)

	body := []byte(`{
		"model": "qwen/qwen3-coder-next",
		"stream": true,
		"tools": [{"type":"function","function":{"name":"get_x","parameters":{"type":"object","properties":{}}}}],
		"messages": [
			{"role":"system","content":"` + bigText("you are helpful") + `"},
			{"role":"user","content":"` + bigText("question") + `"}
		]
	}`)

	out, err := mod(body)
	require.NoError(t, err)

	msgs := parseMessages(t, out)
	require.Len(t, msgs, 2)
	assert.True(t, lastPartHasCacheControl(t, msgs[0]), "system message must be marked")
	assert.True(t, lastPartHasCacheControl(t, msgs[1]), "last (history) message must be marked")

	// system message text is preserved inside the new content part.
	var parts []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(msgs[0]["content"], &parts))
	assert.Contains(t, string(parts[0]["text"]), "you are helpful")

	// tools + model + stream untouched (parsed equal).
	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(out, &top))
	assert.JSONEq(t, `[{"type":"function","function":{"name":"get_x","parameters":{"type":"object","properties":{}}}}]`, string(top["tools"]))
	assert.JSONEq(t, `"qwen/qwen3-coder-next"`, string(top["model"]))
	assert.JSONEq(t, `true`, string(top["stream"]))
}

func TestCacheModifier_ArrayContentMarksLastPart(t *testing.T) {
	mod := NewCacheControlModifier("anthropic", &models.CacheControl{Enabled: true, MinPrefixTokens: 1, Breakpoints: []string{"history"}})
	require.NotNil(t, mod)

	body := []byte(`{
		"messages": [
			{"role":"user","content":[
				{"type":"text","text":"` + bigText("part one") + `"},
				{"type":"text","text":"` + bigText("part two") + `"}
			]}
		]
	}`)

	out, err := mod(body)
	require.NoError(t, err)
	msgs := parseMessages(t, out)
	require.Len(t, msgs, 1)

	var parts []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(msgs[0]["content"], &parts))
	require.Len(t, parts, 2)
	_, firstMarked := parts[0]["cache_control"]
	_, lastMarked := parts[1]["cache_control"]
	assert.False(t, firstMarked, "only the last part is a breakpoint")
	assert.True(t, lastMarked)
}

func TestCacheModifier_SkipsToolCallOnlyMessage(t *testing.T) {
	mod := NewCacheControlModifier("openai_compatible", &models.CacheControl{Enabled: true, MinPrefixTokens: 1, Breakpoints: []string{"history"}})
	require.NotNil(t, mod)

	// Last message is an assistant turn with null content + tool_calls — not
	// cacheable; the history breakpoint must fall back to the prior user turn.
	body := []byte(`{
		"messages": [
			{"role":"user","content":"` + bigText("do something") + `"},
			{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_x","arguments":"{}"}}]}
		]
	}`)

	out, err := mod(body)
	require.NoError(t, err)
	msgs := parseMessages(t, out)
	require.Len(t, msgs, 2)

	assert.True(t, lastPartHasCacheControl(t, msgs[0]), "fell back to the cacheable user turn")
	// tool_calls message left intact: content still null, tool_calls preserved.
	assert.JSONEq(t, `null`, string(msgs[1]["content"]))
	assert.Contains(t, string(msgs[1]["tool_calls"]), "call_1")
}

func TestCacheModifier_HistoryBreakpointOnAppendOnlyTail(t *testing.T) {
	mod := NewCacheControlModifier("openai_compatible", &models.CacheControl{Enabled: true, MinPrefixTokens: 1})
	require.NotNil(t, mod)

	// Engine shape: head system + conversation + a trailing injected system reminder. The
	// request is built append-only (reminders/turns appended at the tail, never rewritten),
	// so the LAST message is byte-stable once written and is the correct history breakpoint:
	// marking it caches the whole growing prefix, and on the next call it is interior and
	// read from cache. Only head + tail carry the breakpoint; interior messages do not.
	body := []byte(`{
		"messages": [
			{"role":"system","content":"` + bigText("you are helpful") + `"},
			{"role":"user","content":"` + bigText("the stable question") + `"},
			{"role":"system","content":"` + bigText("COUNTDOWN only N steps left") + `"}
		]
	}`)

	out, err := mod(body)
	require.NoError(t, err)
	msgs := parseMessages(t, out)
	require.Len(t, msgs, 3)

	assert.True(t, lastPartHasCacheControl(t, msgs[0]), "head system marked")
	assert.False(t, lastPartHasCacheControl(t, msgs[1]), "interior message must not be the breakpoint")
	assert.True(t, lastPartHasCacheControl(t, msgs[2]), "the append-only tail carries the history breakpoint")
}

func TestCacheModifier_MinPrefixGateSkipsSmallPrefix(t *testing.T) {
	mod := NewCacheControlModifier("openai_compatible", &models.CacheControl{Enabled: true, MinPrefixTokens: 100000})
	require.NotNil(t, mod)

	body := []byte(`{"messages":[{"role":"system","content":"tiny"},{"role":"user","content":"hi"}]}`)
	out, err := mod(body)
	require.NoError(t, err)
	assert.JSONEq(t, string(body), string(out), "below gate → no breakpoints injected")
}

func TestCacheModifier_BreakpointConfigPlacement(t *testing.T) {
	body := []byte(`{
		"messages": [
			{"role":"system","content":"` + bigText("sys") + `"},
			{"role":"user","content":"` + bigText("u1") + `"}
		]
	}`)

	t.Run("system only", func(t *testing.T) {
		mod := NewCacheControlModifier("openai_compatible", &models.CacheControl{Enabled: true, MinPrefixTokens: 1, Breakpoints: []string{"system"}})
		out, err := mod(body)
		require.NoError(t, err)
		msgs := parseMessages(t, out)
		assert.True(t, lastPartHasCacheControl(t, msgs[0]))
		assert.False(t, lastPartHasCacheControl(t, msgs[1]), "history not requested")
	})

	t.Run("tools folds into system", func(t *testing.T) {
		mod := NewCacheControlModifier("openai_compatible", &models.CacheControl{Enabled: true, MinPrefixTokens: 1, Breakpoints: []string{"tools"}})
		out, err := mod(body)
		require.NoError(t, err)
		msgs := parseMessages(t, out)
		assert.True(t, lastPartHasCacheControl(t, msgs[0]), "tools breakpoint caches the system block positionally")
	})
}

func TestCacheModifier_PassthroughOnUnexpectedShape(t *testing.T) {
	mod := NewCacheControlModifier("openai_compatible", &models.CacheControl{Enabled: true, MinPrefixTokens: 1})
	require.NotNil(t, mod)

	t.Run("non-JSON", func(t *testing.T) {
		in := []byte("not json at all")
		out, err := mod(in)
		require.NoError(t, err)
		assert.Equal(t, in, out)
	})
	t.Run("no messages key", func(t *testing.T) {
		in := []byte(`{"model":"x","stream":true}`)
		out, err := mod(in)
		require.NoError(t, err)
		assert.Equal(t, in, out)
	})
	t.Run("empty messages", func(t *testing.T) {
		in := []byte(`{"messages":[]}`)
		out, err := mod(in)
		require.NoError(t, err)
		assert.Equal(t, in, out)
	})
	// A content-parts array whose last element is JSON null decodes to a nil map;
	// marking it must not panic — caching is best-effort, the request still ships.
	t.Run("array content with null last part", func(t *testing.T) {
		in := []byte(`{"messages":[{"role":"user","content":[` + `null]}]}`)
		require.NotPanics(t, func() {
			out, err := mod(in)
			require.NoError(t, err)
			assert.Equal(t, in, out, "no markable part → passthrough unchanged")
		})
	})
	t.Run("array content with text then null", func(t *testing.T) {
		in := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"` + bigText("hi") + `"},null]}]}`)
		require.NotPanics(t, func() {
			_, err := mod(in)
			require.NoError(t, err)
		})
	})
}
