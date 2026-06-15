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
	// maxCacheBreakpoints mirrors the provider cap (Anthropic: 4 breakpoints).
	maxCacheBreakpoints = 4
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

	changed := false
	for _, idx := range selectBreakpointIndices(msgs, markSystem, markHistory) {
		if applyCacheControlToMessage(msgs[idx]) {
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

// selectBreakpointIndices picks the message indices to mark (≤ maxCacheBreakpoints),
// deduped. system = first system message; history = last STABLE cacheable message.
// Both placements yield incremental hits: the front caches the large stable block,
// the tail caches the growing conversation prefix.
func selectBreakpointIndices(msgs []map[string]json.RawMessage, markSystem, markHistory bool) []int {
	var idxs []int
	seen := map[int]bool{}
	add := func(i int) {
		if i >= 0 && !seen[i] && len(idxs) < maxCacheBreakpoints {
			seen[i] = true
			idxs = append(idxs, i)
		}
	}
	if markSystem {
		if i := firstSystemIndex(msgs); i >= 0 {
			add(i)
		}
	}
	if markHistory {
		if i := lastStableCacheableIndex(msgs); i >= 0 {
			add(i)
		}
	}
	return idxs
}

func firstSystemIndex(msgs []map[string]json.RawMessage) int {
	for i, m := range msgs {
		if messageRole(m) == "system" {
			return i
		}
	}
	return -1
}

// lastStableCacheableIndex returns the index that ends the stable cacheable prefix:
// the last NON-system message with cacheable content. Trailing system messages are
// per-call dynamic reminders injected after the conversation (tool-call history,
// environment time, finalize/urgency directives) — marking one of them anchors the
// cache breakpoint on content that changes every call, so the write is never re-read
// and only the static head re-hits (the cached_tokens-frozen-at-system symptom). The
// genuine conversation tail (a user/assistant/tool turn) is byte-stable call-to-call.
// Non-cacheable turns (e.g. tool_call-only assistant with null content) are skipped
// by contentIsCacheable.
func lastStableCacheableIndex(msgs []map[string]json.RawMessage) int {
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

// applyCacheControlToMessage adds an ephemeral cache_control marker to a message's
// content, converting a plain string into a single text content-part. Other
// message fields (role, name, tool_calls, tool_call_id) are left untouched.
// Returns whether the message was modified.
func applyCacheControlToMessage(m map[string]json.RawMessage) bool {
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
		part := map[string]json.RawMessage{
			"type":          json.RawMessage(`"text"`),
			"cache_control": ephemeralCacheControl,
		}
		text, err := json.Marshal(s)
		if err != nil {
			return false
		}
		part["text"] = text
		newContent, err := json.Marshal([]map[string]json.RawMessage{part})
		if err != nil {
			return false
		}
		m["content"] = newContent
		return true
	case jsonArray:
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
