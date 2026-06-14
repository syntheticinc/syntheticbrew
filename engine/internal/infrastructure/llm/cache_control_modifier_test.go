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

func TestNewCacheControlModifier_NilWhenOffOrUnsupported(t *testing.T) {
	enabled := &models.CacheControl{Enabled: true}
	disabled := &models.CacheControl{Enabled: false}

	cases := []struct {
		name     string
		provider string
		cc       *models.CacheControl
		wantNil  bool
	}{
		{"nil config", "openai_compatible", nil, true},
		{"disabled", "openai_compatible", disabled, true},
		{"openai automatic", "openai", enabled, true},
		{"azure automatic", "azure_openai", enabled, true},
		{"google automatic", "google", enabled, true},
		{"ollama local", "ollama", enabled, true},
		{"openrouter explicit", "openai_compatible", enabled, false},
		{"anthropic explicit", "anthropic", enabled, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mod := NewCacheControlModifier(tc.provider, tc.cc)
			if tc.wantNil {
				assert.Nil(t, mod, "modifier must be nil → not attached → byte-identical request")
			} else {
				assert.NotNil(t, mod)
			}
		})
	}
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

func TestCacheModifier_HistorySkipsTrailingSystemReminders(t *testing.T) {
	mod := NewCacheControlModifier("openai_compatible", &models.CacheControl{Enabled: true, MinPrefixTokens: 1})
	require.NotNil(t, mod)

	// Engine shape: head system + conversation + a trailing injected system reminder
	// (tool-call history / environment) whose content changes every call. The history
	// breakpoint must land on the last STABLE message (the user turn), NOT the dynamic
	// trailing reminder — otherwise that cache block is never re-read and only the head
	// caches (the cached_tokens-frozen-at-system symptom).
	body := []byte(`{
		"messages": [
			{"role":"system","content":"` + bigText("you are helpful") + `"},
			{"role":"user","content":"` + bigText("the stable question") + `"},
			{"role":"system","content":"` + bigText("TOOL HISTORY changes every step") + `"}
		]
	}`)

	out, err := mod(body)
	require.NoError(t, err)
	msgs := parseMessages(t, out)
	require.Len(t, msgs, 3)

	assert.True(t, lastPartHasCacheControl(t, msgs[0]), "head system marked")
	assert.True(t, lastPartHasCacheControl(t, msgs[1]), "history breakpoint on the stable user turn")
	assert.False(t, lastPartHasCacheControl(t, msgs[2]), "trailing dynamic reminder must NOT be the breakpoint")
}

// TestCacheModifier_AdversarialNoPanic throws malformed/attacker-influenceable
// message shapes at the modifier and asserts it NEVER panics and NEVER errors the
// request (SCC-03: best-effort passthrough). Each case pairs a large stable system
// head (so the min-prefix gate is cleared and the marking write-paths are actually
// exercised) with an adversarial trailing message that becomes the history breakpoint.
func TestCacheModifier_AdversarialNoPanic(t *testing.T) {
	mod := NewCacheControlModifier("openai_compatible", &models.CacheControl{Enabled: true, MinPrefixTokens: 1})
	require.NotNil(t, mod)
	sys := `{"role":"system","content":"` + bigText("head") + `"}`

	adversarial := map[string]string{
		"content object":            `{"role":"user","content":{"x":1}}`,
		"content number":            `{"role":"user","content":42}`,
		"content bool":              `{"role":"user","content":true}`,
		"content null":              `{"role":"user","content":null}`,
		"array null only":           `{"role":"user","content":[null]}`,
		"array null last":           `{"role":"user","content":[{"type":"text","text":"a"},null]}`,
		"array null first":          `{"role":"user","content":[null,{"type":"text","text":"a"}]}`,
		"array mixed null scalar":   `{"role":"user","content":[{"type":"text","text":"a"},null,123]}`,
		"array of strings":          `{"role":"user","content":["a","b"]}`,
		"array of numbers":          `{"role":"user","content":[1,2,3]}`,
		"array empty":               `{"role":"user","content":[]}`,
		"role number":              `{"role":5,"content":"` + bigText("x") + `"}`,
		"role null":                `{"role":null,"content":"` + bigText("x") + `"}`,
		"no role":                  `{"content":"` + bigText("x") + `"}`,
		"cache_control preexisting": `{"role":"user","content":[{"type":"text","text":"a","cache_control":{"type":"ephemeral"}}]}`,
		"nested parts":              `{"role":"user","content":[{"type":"text","text":"a","extra":{"deep":{"deeper":[1,2,3]}}}]}`,
		"control chars":             `{"role":"user","content":"ab"}`,
		"part missing type":         `{"role":"user","content":[{"text":"a"}]}`,
		"part type number":          `{"role":"user","content":[{"type":7,"text":"a"}]}`,
	}
	for name, msg := range adversarial {
		t.Run(name, func(t *testing.T) {
			body := []byte(`{"model":"m","messages":[` + sys + `,` + msg + `]}`)
			require.NotPanics(t, func() {
				out, err := mod(body)
				require.NoError(t, err, "malformed input must pass through, never error")
				require.NotEmpty(t, out)
			})
		})
	}

	// Adversarial content on the FIRST system message (exercises the markSystem write
	// path, not just the history one).
	t.Run("system head array null last", func(t *testing.T) {
		body := []byte(`{"messages":[{"role":"system","content":[{"type":"text","text":"` + bigText("h") + `"},null]},{"role":"user","content":"` + bigText("q") + `"}]}`)
		require.NotPanics(t, func() {
			out, err := mod(body)
			require.NoError(t, err)
			require.NotEmpty(t, out)
		})
	})

	// DoS: a large message array must complete quickly and stay bounded (≤4 markers).
	t.Run("huge message array bounded", func(t *testing.T) {
		var sb strings.Builder
		sb.WriteString(`{"messages":[`)
		sb.WriteString(sys)
		for i := 0; i < 5000; i++ {
			sb.WriteString(`,{"role":"user","content":"hello world filler text here"}`)
		}
		sb.WriteString(`]}`)
		require.NotPanics(t, func() {
			out, err := mod([]byte(sb.String()))
			require.NoError(t, err)
			// at most maxCacheBreakpoints content blocks were rewritten to arrays.
			require.LessOrEqual(t, strings.Count(string(out), `"cache_control"`), maxCacheBreakpoints)
		})
	})
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
