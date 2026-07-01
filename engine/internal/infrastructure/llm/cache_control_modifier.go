package llm

import (
	"encoding/json"
	"fmt"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// Provider-agnostic prompt-cache control.
//
// The engine never models cache_control natively (eino-ext doesn't either), so we
// mark the stable prefix by post-processing the serialized chat-completion body
// through eino-ext's WithRequestPayloadModifier seam. Only explicit-cache adapters
// honor wire breakpoints; automatic-cache providers cache on their own and are not
// touched (the modifier is simply not attached for them).

const (
	// defaultCacheMinPrefixTokens guards against marking prefixes below the
	// provider cache minimum (~1024 tokens for Anthropic/Qwen): marking a tiny
	// prefix only pays the cache-write premium with no read benefit.
	defaultCacheMinPrefixTokens = 1024
	// cacheGateCharsPerToken converts the token gate to a char budget for the
	// body-size estimate. Conservative (biases toward not-caching small prefixes).
	cacheGateCharsPerToken = 4
)

var ephemeralCacheControl = json.RawMessage(`{"type":"ephemeral"}`)

// providerHonorsCacheControl reports whether an adapter translates explicit
// cache_control breakpoints to the wire. openai/azure/google cache automatically
// (≥ a threshold) and ignore the marker; ollama is local.
func providerHonorsCacheControl(providerType string) bool {
	switch providerType {
	case "openai_compatible", "anthropic":
		return true
	default:
		return false
	}
}

// NewCacheControlModifier returns a stateless request-payload modifier that marks
// the stable prefix of an OpenAI-compatible chat-completion body with
// cache_control:{type:ephemeral} breakpoints. For providers that honor explicit
// breakpoints (openai_compatible, anthropic) caching is default-on: an absent
// config (cc == nil) is treated as enabled with default breakpoints and min-prefix.
// A tenant opts out with an explicit cache_control.enabled=false, which returns nil.
// Providers that don't honor explicit breakpoints always return nil. Callers must
// treat nil as "do not attach a modifier" so the request stays byte-identical to
// no-cache. The returned func operates only on its input bytes and is safe for
// concurrent use.
func NewCacheControlModifier(providerType string, cc *models.CacheControl) func([]byte) ([]byte, error) {
	if !providerHonorsCacheControl(providerType) {
		return nil
	}
	// Default-on: absent config caches with adapter defaults.
	if cc == nil {
		cc = &models.CacheControl{Enabled: true}
	}
	if !cc.Enabled {
		return nil
	}
	minTokens := cc.MinPrefixTokens
	if minTokens <= 0 {
		minTokens = defaultCacheMinPrefixTokens
	}
	markSystem, markHistory := resolveBreakpoints(cc.Breakpoints)
	return func(body []byte) ([]byte, error) {
		return injectCacheControl(body, minTokens, markSystem, markHistory)
	}
}

// resolveBreakpoints maps configured breakpoint names to placement flags. Empty =
// default placement (system + last stable turn). "tools" folds into the system
// breakpoint: on the wire, tools render before the system block, so a system
// breakpoint already caches the tool definitions positionally.
func resolveBreakpoints(names []string) (markSystem, markHistory bool) {
	if len(names) == 0 {
		return true, true
	}
	for _, n := range names {
		switch n {
		case "system", "tools":
			markSystem = true
		case "history":
			markHistory = true
		}
	}
	return markSystem, markHistory
}

// injectCacheControl rewrites the `messages` array of an OpenAI-compatible body,
// marking the chosen prefix messages. Non-touched top-level fields (tools, model,
// stream, …) are preserved as raw bytes. Any unexpected shape passes through
// untouched rather than erroring — caching is best-effort, never request-breaking.
func injectCacheControl(body []byte, minPrefixTokens int, markSystem, markHistory bool) ([]byte, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return body, nil
	}
	rawMsgs, ok := top["messages"]
	if !ok {
		return body, nil
	}
	var msgs []map[string]json.RawMessage
	if err := json.Unmarshal(rawMsgs, &msgs); err != nil || len(msgs) == 0 {
		return body, nil
	}

	if prefixCharCount(msgs) < minPrefixTokens*cacheGateCharsPerToken {
		return body, nil
	}

	// Canonicalize every string-content message to array (single text part) form. A cache
	// breakpoint requires array content, so a marked message is array; without canonicalizing
	// the rest, a message that was the breakpoint one step ago (array) and is interior the
	// next (string) would change shape and break the byte-stable prefix that explicit-cache
	// providers require — the request is built append-only, so the breakpoint moves to the
	// new tail each step and the previous tail becomes interior. With every message in array
	// form, a former breakpoint stays array and only the cache_control field drops off, which
	// the providers tolerate.
	markSet := make(map[int]bool)
	for _, idx := range selectBreakpointIndices(msgs, markSystem, markHistory) {
		markSet[idx] = true
	}
	changed := false
	for i := range msgs {
		if canonicalizeContent(msgs[i], markSet[i]) {
			changed = true
		}
	}
	if !changed {
		return body, nil
	}

	newMsgs, err := json.Marshal(msgs)
	if err != nil {
		return nil, fmt.Errorf("cache_control: marshal messages: %w", err)
	}
	top["messages"] = newMsgs
	out, err := json.Marshal(top)
	if err != nil {
		return nil, fmt.Errorf("cache_control: marshal body: %w", err)
	}
	return out, nil
}

// selectBreakpointIndices picks the ≤2 cache breakpoints, deduped: the HEAD (first system
// message — the turn-invariant stable prefix, byte-stable across turns so it caches
// cross-turn) and the moving TAIL (the last stable conversation message, skipping the
// trailing volatile head). As the append-only conversation grows the tail breakpoint moves
// forward one step at a time; each request's tail marker chains to the PREVIOUS request's
// tail cache write (a few content blocks back, within the provider's extend window), so the
// whole prefix stays cached to ANY depth — there is no fixed-checkpoint cap. Canonicalizing
// every message to array form keeps a former tail byte-stable when the marker moves off it
// (only the cache_control field drops, which the providers tolerate — proven live on
// qwen3.7-plus: cached tracks the full prefix across depth with no collapse, whereas a
// fixed-stride cap froze cached at the ~48th message).
func selectBreakpointIndices(msgs []map[string]json.RawMessage, markSystem, markHistory bool) []int {
	var idxs []int
	seen := map[int]bool{}
	add := func(i int) {
		if i >= 0 && !seen[i] {
			seen[i] = true
			idxs = append(idxs, i)
		}
	}
	if markSystem {
		add(firstSystemIndex(msgs))
	}
	if markHistory {
		add(lastCacheableConversationIndex(msgs))
	}
	return idxs
}

// lastCacheableConversationIndex returns the index of the last cacheable message that is
// part of the append-only conversation, skipping trailing system messages (the per-turn
// volatile head), or -1 if none.
func lastCacheableConversationIndex(msgs []map[string]json.RawMessage) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		if messageRole(msgs[i]) == "system" {
			continue
		}
		if contentIsCacheable(msgs[i]["content"]) {
			return i
		}
	}
	return -1
}

func firstSystemIndex(msgs []map[string]json.RawMessage) int {
	for i, m := range msgs {
		if messageRole(m) == "system" {
			return i
		}
	}
	return -1
}

func messageRole(m map[string]json.RawMessage) string {
	raw, ok := m["role"]
	if !ok {
		return ""
	}
	var role string
	if err := json.Unmarshal(raw, &role); err != nil {
		return ""
	}
	return role
}

// contentIsCacheable reports whether a message's content can carry a breakpoint —
// a non-empty string or a non-empty content-parts array. null/absent content
// (e.g. an assistant turn that is only tool_calls) cannot.
func contentIsCacheable(content json.RawMessage) bool {
	t := jsonKind(content)
	if t == jsonString {
		var s string
		return json.Unmarshal(content, &s) == nil && s != ""
	}
	if t == jsonArray {
		var parts []json.RawMessage
		return json.Unmarshal(content, &parts) == nil && len(parts) > 0
	}
	return false
}

// canonicalizeContent rewrites a message's content to array form — a string becomes a
// single text content-part — and, when mark is true, adds an ephemeral cache_control
// marker to the last content-part. Canonicalizing EVERY message (not just the marked
// ones) keeps shape stable across calls: a message that is the breakpoint one step and
// interior the next stays array either way, so only the cache_control field changes.
// Other message fields (role, name, tool_calls, tool_call_id) are left untouched.
// null/empty content (e.g. a tool_calls-only assistant turn) is left as-is. Returns
// whether the message was modified.
func canonicalizeContent(m map[string]json.RawMessage, mark bool) bool {
	content, ok := m["content"]
	if !ok {
		return false
	}
	switch jsonKind(content) {
	case jsonString:
		var s string
		if err := json.Unmarshal(content, &s); err != nil || s == "" {
			return false
		}
		text, err := json.Marshal(s)
		if err != nil {
			return false
		}
		part := map[string]json.RawMessage{
			"type": json.RawMessage(`"text"`),
			"text": text,
		}
		if mark {
			part["cache_control"] = ephemeralCacheControl
		}
		newContent, err := json.Marshal([]map[string]json.RawMessage{part})
		if err != nil {
			return false
		}
		m["content"] = newContent
		return true
	case jsonArray:
		if !mark {
			return false // already array form; nothing to change when not marking
		}
		var parts []map[string]json.RawMessage
		if err := json.Unmarshal(content, &parts); err != nil || len(parts) == 0 {
			return false
		}
		// A JSON null element decodes to a nil map (e.g. content:[null]); writing to
		// it would panic. Caching is best-effort — skip rather than break the request.
		last := parts[len(parts)-1]
		if last == nil {
			return false
		}
		last["cache_control"] = ephemeralCacheControl
		newContent, err := json.Marshal(parts)
		if err != nil {
			return false
		}
		m["content"] = newContent
		return true
	default:
		return false
	}
}

// prefixCharCount estimates the cacheable prefix size from message content length.
func prefixCharCount(msgs []map[string]json.RawMessage) int {
	total := 0
	for _, m := range msgs {
		if c, ok := m["content"]; ok {
			total += len(c)
		}
	}
	return total
}

type jsonValueKind int

const (
	jsonOther jsonValueKind = iota
	jsonString
	jsonArray
)

// jsonKind classifies a raw JSON value by its first non-space byte.
func jsonKind(raw json.RawMessage) jsonValueKind {
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		case '"':
			return jsonString
		case '[':
			return jsonArray
		default:
			return jsonOther
		}
	}
	return jsonOther
}
