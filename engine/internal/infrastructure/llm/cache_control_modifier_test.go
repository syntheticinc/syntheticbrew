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

// markedHistoryIndices returns the message indices carrying a cache_control marker,
// for a history-only modifier run over a body of n cacheable text messages.
func markedHistoryIndices(t *testing.T, n int) []int {
	t.Helper()
	mod := NewCacheControlModifier("openai_compatible", &models.CacheControl{Enabled: true, MinPrefixTokens: 1, Breakpoints: []string{"history"}})
	require.NotNil(t, mod)
	var sb strings.Builder
	sb.WriteString(`{"messages":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"role":"user","content":"` + bigText("m") + `"}`)
	}
	sb.WriteString(`]}`)
	out, err := mod([]byte(sb.String()))
	require.NoError(t, err)
	msgs := parseMessages(t, out)
	var marked []int
	for i, m := range msgs {
		if lastPartHasCacheControl(t, m) {
			marked = append(marked, i)
		}
	}
	return marked
}

// TestCacheModifier_MovingTailFollowsConversation is the regression guard for the
// full-depth cache fix: the history breakpoint is a SINGLE marker on the last cacheable
// message (the moving tail) that follows the append-only conversation to ANY depth — no
// fixed-stride checkpoints and no 4-breakpoint cap. Each step's tail marker chains to the
// previous request's tail cache write (a few blocks back, within the provider's extend
// window), so the whole prefix stays cached; on qwen3.7-plus this cached ~97% of a 30k+
// prompt at depth where the old fixed-stride scheme froze cached at the ~48th message.
func TestCacheModifier_MovingTailFollowsConversation(t *testing.T) {
	// The single breakpoint is always the LAST message (the moving tail), at every depth.
	require.Equal(t, []int{19}, markedHistoryIndices(t, 20), "tail marker on the last message")
	require.Equal(t, []int{23}, markedHistoryIndices(t, 24), "tail marker moves forward with the conversation")
	require.Equal(t, []int{39}, markedHistoryIndices(t, 40), "tail marker follows to any depth — no stride cap")
	require.Equal(t, []int{3}, markedHistoryIndices(t, 4), "short conversation marks its tail too")
}

func TestCacheModifier_HistoryBreakpointSkipsTrailingVolatileHead(t *testing.T) {
	mod := NewCacheControlModifier("openai_compatible", &models.CacheControl{Enabled: true, MinPrefixTokens: 1})
	require.NotNil(t, mod)

	// Engine shape: stable head (system) + conversation + the per-turn VOLATILE head — a
	// TRAILING system message (CURRENT TASK + reminders). The volatile head is ephemeral: its
	// array slot is overwritten by a real conversation message on the next step, so it is NOT
	// byte-stable at its position and must NOT carry the history breakpoint (marking it would
	// move the marker onto changing bytes and collapse the within-turn cache). The short-turn
	// breakpoint anchors on the last byte-stable CONVERSATION message (msg 1, the user turn);
	// the trailing volatile head is skipped.
	body := []byte(`{
		"messages": [
			{"role":"system","content":"` + bigText("you are helpful") + `"},
			{"role":"user","content":"` + bigText("the stable question") + `"},
			{"role":"system","content":"` + bigText("CURRENT TASK answer the question") + `"}
		]
	}`)

	out, err := mod(body)
	require.NoError(t, err)
	msgs := parseMessages(t, out)
	require.Len(t, msgs, 3)

	assert.True(t, lastPartHasCacheControl(t, msgs[0]), "stable head system marked")
	assert.True(t, lastPartHasCacheControl(t, msgs[1]), "the last CONVERSATION message carries the history breakpoint")
	assert.False(t, lastPartHasCacheControl(t, msgs[2]), "the trailing volatile head must NOT be the breakpoint (ephemeral; its slot is overwritten next step)")
}

// TestCacheModifier_DeepHistoryMarksHeadAndTailOnly is the deterministic guard that on a
// LONG conversation the marker set is exactly {head, moving tail} at ANY depth — the head
// (index 0) and the last cacheable CONVERSATION message — with no fixed-stride checkpoints,
// no 4-breakpoint cap, and the trailing volatile head never marked. Critically, the recent
// tail IS marked (it's the breakpoint), so — unlike the old stride scheme that left every
// message past checkpoint 48 unmarked and re-billed at depth — the whole prefix caches to
// full depth.
func TestCacheModifier_DeepHistoryMarksHeadAndTailOnly(t *testing.T) {
	mod := NewCacheControlModifier("openai_compatible", &models.CacheControl{Enabled: true, MinPrefixTokens: 1})
	require.NotNil(t, mod)

	// Engine shape: system head at index 0, then ~78 alternating user/assistant/tool
	// conversation messages, then the per-turn VOLATILE head (trailing system message) at the
	// last index. Every message carries enough text to clear the min-prefix gate.
	const convCount = 78
	var sb strings.Builder
	sb.WriteString(`{"messages":[`)
	sb.WriteString(`{"role":"system","content":"` + bigText("stable head system prompt") + `"}`)
	roles := []string{"user", "assistant", "tool"}
	for i := 0; i < convCount; i++ {
		sb.WriteString(`,{"role":"` + roles[i%len(roles)] + `","content":"` + bigText("conversation message") + `"}`)
	}
	// Trailing volatile head: a system message (CURRENT TASK + reminders), overwritten by a
	// real conversation message next step, so it must NOT be a breakpoint.
	sb.WriteString(`,{"role":"system","content":"` + bigText("CURRENT TASK answer the question") + `"}`)
	sb.WriteString(`]}`)

	out, err := mod([]byte(sb.String()))
	require.NoError(t, err)
	msgs := parseMessages(t, out)
	require.Len(t, msgs, 1+convCount+1) // head + conversation + volatile head

	var marked []int
	for i, m := range msgs {
		if lastPartHasCacheControl(t, m) {
			marked = append(marked, i)
		}
	}

	// Exactly 2 markers: the head (0) and the moving tail — the last CONVERSATION message
	// (array index convCount, since the trailing volatile head at convCount+1 is skipped).
	// No fixed-stride indices (16/32/48), and NOT capped away from the deep tail.
	tail := convCount // 1 (head) + convCount conversation messages ⇒ last conv index == convCount
	assert.Equal(t, []int{0, tail}, marked,
		"marked set must be exactly {head, moving tail} at any depth — no stride checkpoints, no cap")

	// The trailing volatile head (last message) must NOT be marked.
	assert.False(t, lastPartHasCacheControl(t, msgs[len(msgs)-1]),
		"the trailing volatile head must never carry a breakpoint (ephemeral; overwritten next step)")

	// Only the head and the tail are breakpoints — every interior message is read from cache.
	for i := 1; i < tail; i++ {
		assert.False(t, lastPartHasCacheControl(t, msgs[i]),
			"interior message %d must not be marked (only head + moving tail are breakpoints)", i)
	}
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
