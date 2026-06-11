package agents

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestNewContextRewriter(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name           string
		maxContextSize int
		input          []*schema.Message
		wantLength     int
	}{
		{
			name:           "empty input",
			maxContextSize: 1000,
			input:          []*schema.Message{},
			wantLength:     0,
		},
		{
			name:           "context within limit",
			maxContextSize: 10000,
			input: []*schema.Message{
				{Role: schema.System, Content: "System prompt"},
				{Role: schema.User, Content: "User message"},
				{Role: schema.Assistant, Content: "Assistant response"},
			},
			wantLength: 3,
		},
		{
			name:           "context exceeds limit",
			maxContextSize: 50,
			input: []*schema.Message{
				{Role: schema.System, Content: "System prompt"},
				{Role: schema.User, Content: "User message 1"},
				{Role: schema.Assistant, Content: "Assistant response 1"},
				{Role: schema.User, Content: "User message 2"},
				{Role: schema.Assistant, Content: "Assistant response 2"},
			},
			wantLength: 5, // System + ALL user messages + assistant responses (small enough to fit)
		},
		{
			name:           "context exactly at limit",
			maxContextSize: 20,
			input: []*schema.Message{
				{Role: schema.System, Content: "System"},
				{Role: schema.User, Content: "User"},
			},
			wantLength: 2,
		},
		{
			name:           "small input within limit",
			maxContextSize: 1000,
			input: []*schema.Message{
				{Role: schema.System, Content: "System"},
			},
			wantLength: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rewriter := NewContextRewriter(tt.maxContextSize)
			result := rewriter(ctx, tt.input)

			if len(result) != tt.wantLength {
				t.Errorf("NewContextRewriter() returned %d messages, want %d", len(result), tt.wantLength)
			}

			// Verify first message is preserved (system prompt)
			if len(tt.input) > 0 && len(result) > 0 {
				if result[0].Role != tt.input[0].Role {
					t.Errorf("NewContextRewriter() first message role = %v, want %v", result[0].Role, tt.input[0].Role)
				}
				if result[0].Content != tt.input[0].Content {
					t.Errorf("NewContextRewriter() first message content = %v, want %v", result[0].Content, tt.input[0].Content)
				}
			}
		})
	}
}

// TestContextRewriter_PreservesAllUserMessages verifies that ALL user messages are kept
func TestContextRewriter_PreservesAllUserMessages(t *testing.T) {
	ctx := context.Background()

	// Create input with multiple user messages
	input := []*schema.Message{
		{Role: schema.System, Content: "System"},
		{Role: schema.User, Content: "Question 1"},
		{Role: schema.Assistant, Content: "Answer 1 - this is a very long response that takes up a lot of space in the context window"},
		{Role: schema.User, Content: "Question 2"},
		{Role: schema.Assistant, Content: "Answer 2 - another very long response that takes up a lot of space in the context window"},
		{Role: schema.User, Content: "Question 3"},
		{Role: schema.Assistant, Content: "Answer 3 - yet another very long response that takes up a lot of space"},
	}

	// Use a small limit to force compression
	rewriter := NewContextRewriter(50) // 50 tokens = 200 chars
	result := rewriter(ctx, input)

	// Count user messages in result
	userCount := 0
	for _, msg := range result {
		if msg.Role == schema.User {
			userCount++
		}
	}

	// All 3 user messages should be preserved
	if userCount != 3 {
		t.Errorf("PreservesAllUserMessages: found %d user messages, want 3", userCount)
	}

	// Verify all user messages are present with correct content
	userContents := make(map[string]bool)
	for _, msg := range result {
		if msg.Role == schema.User {
			userContents[msg.Content] = true
		}
	}

	for _, expected := range []string{"Question 1", "Question 2", "Question 3"} {
		if !userContents[expected] {
			t.Errorf("PreservesAllUserMessages: missing user message '%s'", expected)
		}
	}
}

// TestContextRewriter_ToolPairsKeptTogether verifies Assistant+Tool pairs are not separated
func TestContextRewriter_ToolPairsKeptTogether(t *testing.T) {
	ctx := context.Background()

	input := []*schema.Message{
		{Role: schema.System, Content: "System"},
		{Role: schema.User, Content: "Search for code"},
		{
			Role:    schema.Assistant,
			Content: "",
			ToolCalls: []schema.ToolCall{
				{ID: "call-1", Function: schema.FunctionCall{Name: "search_code", Arguments: `{"query":"test"}`}},
			},
		},
		{Role: schema.Tool, Content: "Found 5 results", Name: "search_code", ToolCallID: "call-1"},
		{Role: schema.Assistant, Content: "I found 5 results for your search."},
	}

	// Use a limit that can fit everything
	rewriter := NewContextRewriter(10000)
	result := rewriter(ctx, input)

	// Should have all 5 messages
	if len(result) != 5 {
		t.Errorf("ToolPairsKeptTogether: got %d messages, want 5", len(result))
	}

	// Find the assistant with tool calls and verify tool result follows
	for i, msg := range result {
		if msg.Role == schema.Assistant && len(msg.ToolCalls) > 0 {
			if i+1 >= len(result) {
				t.Error("ToolPairsKeptTogether: tool result not found after assistant with tool calls")
				continue
			}
			nextMsg := result[i+1]
			if nextMsg.Role != schema.Tool {
				t.Errorf("ToolPairsKeptTogether: expected Tool after Assistant with ToolCalls, got %v", nextMsg.Role)
			}
		}
	}
}

// TestContextRewriter_OrphanedToolRemoved verifies orphaned tool messages are removed during compression
func TestContextRewriter_OrphanedToolRemoved(t *testing.T) {
	ctx := context.Background()

	// Create a scenario where compression must happen and orphaned tool should be removed
	input := []*schema.Message{
		{Role: schema.System, Content: "System prompt that is reasonably long to take up space"},
		{Role: schema.User, Content: "This is a question from the user that takes up space in the context window"},
		// Orphaned tool result (no matching assistant with tool_calls)
		{Role: schema.Tool, Content: "Orphaned result that should be removed during compression because it has no matching assistant", Name: "search_code", ToolCallID: "orphan-call"},
		{Role: schema.Assistant, Content: "This is a regular assistant response without any tool calls"},
	}

	// Use very small limit to force compression
	rewriter := NewContextRewriter(30) // 30 tokens = 120 chars, very tight
	result := rewriter(ctx, input)

	// Should not contain the orphaned tool after compression
	for _, msg := range result {
		if msg.Role == schema.Tool && msg.ToolCallID == "orphan-call" {
			t.Error("OrphanedToolRemoved: orphaned tool message should have been removed during compression")
		}
	}

	// Should still have system prompt and user messages
	hasSystem := false
	hasUser := false
	for _, msg := range result {
		if msg.Role == schema.System {
			hasSystem = true
		}
		if msg.Role == schema.User {
			hasUser = true
		}
	}
	if !hasSystem {
		t.Error("OrphanedToolRemoved: system prompt should be preserved")
	}
	if !hasUser {
		t.Error("OrphanedToolRemoved: user message should be preserved")
	}
}

// TestContextRewriter_OrphanedAssistantWithToolCallsRemoved verifies orphaned assistant+tool_calls are removed during compression
func TestContextRewriter_OrphanedAssistantWithToolCallsRemoved(t *testing.T) {
	ctx := context.Background()

	// Create a scenario where compression must happen
	input := []*schema.Message{
		{Role: schema.System, Content: "System prompt that takes up a reasonable amount of space in the context"},
		{Role: schema.User, Content: "This is a user question that also takes up space in the context window"},
		{
			Role:    schema.Assistant,
			Content: "This assistant message has tool calls but no matching tool result - it is orphaned",
			ToolCalls: []schema.ToolCall{
				{ID: "orphan-call", Function: schema.FunctionCall{Name: "search_code"}},
			},
		},
		// No matching tool result!
		{Role: schema.Assistant, Content: "This is a regular assistant response that comes after the orphaned one"},
	}

	// Use very small limit to force compression
	rewriter := NewContextRewriter(30) // Very tight limit
	result := rewriter(ctx, input)

	// Should not contain the orphaned assistant with tool calls after compression
	for _, msg := range result {
		if msg.Role == schema.Assistant && len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				if tc.ID == "orphan-call" {
					t.Error("OrphanedAssistantWithToolCallsRemoved: orphaned assistant with tool calls should have been removed during compression")
				}
			}
		}
	}

	// Should still have system prompt and user messages
	hasSystem := false
	hasUser := false
	for _, msg := range result {
		if msg.Role == schema.System {
			hasSystem = true
		}
		if msg.Role == schema.User {
			hasUser = true
		}
	}
	if !hasSystem {
		t.Error("OrphanedAssistantWithToolCallsRemoved: system prompt should be preserved")
	}
	if !hasUser {
		t.Error("OrphanedAssistantWithToolCallsRemoved: user message should be preserved")
	}
}

// TestContextRewriter_MultipleToolCallsPerAssistant verifies assistant with multiple tool calls
func TestContextRewriter_MultipleToolCallsPerAssistant(t *testing.T) {
	ctx := context.Background()

	input := []*schema.Message{
		{Role: schema.System, Content: "System"},
		{Role: schema.User, Content: "Search and read files"},
		{
			Role:    schema.Assistant,
			Content: "I'll search and read the files",
			ToolCalls: []schema.ToolCall{
				{ID: "call-1", Function: schema.FunctionCall{Name: "search_code", Arguments: `{"query":"test"}`}},
				{ID: "call-2", Function: schema.FunctionCall{Name: "read_file", Arguments: `{"path":"test.go"}`}},
			},
		},
		{Role: schema.Tool, Content: "Found 5 results", Name: "search_code", ToolCallID: "call-1"},
		{Role: schema.Tool, Content: "File content here", Name: "read_file", ToolCallID: "call-2"},
		{Role: schema.Assistant, Content: "Here are the results..."},
	}

	// Use a large limit to keep everything
	rewriter := NewContextRewriter(10000)
	result := rewriter(ctx, input)

	// Should have all 6 messages
	if len(result) != 6 {
		t.Errorf("MultipleToolCallsPerAssistant: got %d messages, want 6", len(result))
	}

	// Verify both tool results are present
	toolResults := 0
	for _, msg := range result {
		if msg.Role == schema.Tool {
			toolResults++
		}
	}
	if toolResults != 2 {
		t.Errorf("MultipleToolCallsPerAssistant: got %d tool results, want 2", toolResults)
	}
}

// TestContextRewriter_ChronologicalOrderPreserved verifies order is maintained after compression
func TestContextRewriter_ChronologicalOrderPreserved(t *testing.T) {
	ctx := context.Background()

	input := []*schema.Message{
		{Role: schema.System, Content: "System"},
		{Role: schema.User, Content: "Q1"},
		{Role: schema.Assistant, Content: "A1"},
		{Role: schema.User, Content: "Q2"},
		{Role: schema.Assistant, Content: "A2"},
	}

	rewriter := NewContextRewriter(10000)
	result := rewriter(ctx, input)

	// Verify order: System, User (Q1), Assistant (A1), User (Q2), Assistant (A2)
	if len(result) != 5 {
		t.Fatalf("ChronologicalOrderPreserved: got %d messages, want 5", len(result))
	}

	// First should be system
	if result[0].Role != schema.System {
		t.Errorf("ChronologicalOrderPreserved: first message should be System, got %v", result[0].Role)
	}
}

// TestContextRewriter_InterleavedOrderAfterCompression verifies U1->A1->U2->A2 order is preserved
// This is CRITICAL: after compression, the order must NOT become U1->U2->A1->A2
func TestContextRewriter_InterleavedOrderAfterCompression(t *testing.T) {
	ctx := context.Background()

	// Create input that will trigger compression
	// Use messages with enough content to exceed limit
	input := []*schema.Message{
		{Role: schema.System, Content: "Sys"},
		{Role: schema.User, Content: "Q1: First question from user"},
		{Role: schema.Assistant, Content: "A1: First answer to the first question with some details"},
		{Role: schema.User, Content: "Q2: Second question from user"},
		{Role: schema.Assistant, Content: "A2: Second answer with information"},
		{Role: schema.User, Content: "Q3: Third question from user"},
		{Role: schema.Assistant, Content: "A3: Third answer to complete the conversation"},
	}

	// Use limit that allows all messages (no compression)
	rewriter := NewContextRewriter(10000)
	result := rewriter(ctx, input)

	if len(result) != 7 {
		t.Fatalf("InterleavedOrderAfterCompression: got %d messages, want 7", len(result))
	}

	// Verify the order is: System, U1, A1, U2, A2, U3, A3 (interleaved)
	// NOT: System, U1, U2, U3, A1, A2, A3 (grouped)
	expectedOrder := []struct {
		role    schema.RoleType
		content string
	}{
		{schema.System, "Sys"},
		{schema.User, "Q1"},
		{schema.Assistant, "A1"},
		{schema.User, "Q2"},
		{schema.Assistant, "A2"},
		{schema.User, "Q3"},
		{schema.Assistant, "A3"},
	}

	for i, expected := range expectedOrder {
		if result[i].Role != expected.role {
			t.Errorf("Message %d: role = %v, want %v", i, result[i].Role, expected.role)
		}
		if !strings.Contains(result[i].Content, expected.content) {
			t.Errorf("Message %d: content = %q, want to contain %q", i, result[i].Content, expected.content)
		}
	}
}

// TestContextRewriter_CompressedInterleavedOrder verifies order when compression actually happens
func TestContextRewriter_CompressedInterleavedOrder(t *testing.T) {
	ctx := context.Background()

	// Create input where compression will remove some assistant messages
	input := []*schema.Message{
		{Role: schema.System, Content: "S"}, // 1 char
		{Role: schema.User, Content: "Q1"},  // 2 chars
		{Role: schema.Assistant, Content: "A1 - this is a very long response that takes up lots of space"}, // ~60 chars
		{Role: schema.User, Content: "Q2"},  // 2 chars
		{Role: schema.Assistant, Content: "A2 - another long response with lots of text content here"}, // ~60 chars
		{Role: schema.User, Content: "Q3"},  // 2 chars
		{Role: schema.Assistant, Content: "A3"}, // 2 chars - most recent, should be kept
	}

	// Limit that allows system + all users + some assistants but not all
	// Total user content: ~7 chars, system: 1 char = 8 chars reserved
	// Remaining for assistants: needs to fit at least A3
	rewriter := NewContextRewriter(15) // 15 tokens = 60 chars

	result := rewriter(ctx, input)

	// Should have: System, Q1, Q2, Q3, and A3 (most recent assistant)
	// A1 and A2 may be dropped due to size

	// Check that all user messages are present
	userCount := 0
	for _, msg := range result {
		if msg.Role == schema.User {
			userCount++
		}
	}
	if userCount != 3 {
		t.Errorf("CompressedInterleavedOrder: expected 3 user messages, got %d", userCount)
	}

	// CRITICAL: Check that Q3 comes BEFORE A3 (not all Qs before all As)
	q3Idx := -1
	a3Idx := -1
	for i, msg := range result {
		if msg.Role == schema.User && strings.Contains(msg.Content, "Q3") {
			q3Idx = i
		}
		if msg.Role == schema.Assistant && strings.Contains(msg.Content, "A3") {
			a3Idx = i
		}
	}

	if q3Idx == -1 {
		t.Error("CompressedInterleavedOrder: Q3 not found in result")
	}
	if a3Idx == -1 {
		t.Error("CompressedInterleavedOrder: A3 not found in result")
	}
	if q3Idx != -1 && a3Idx != -1 && q3Idx > a3Idx {
		t.Errorf("CompressedInterleavedOrder: Q3 (idx=%d) should come BEFORE A3 (idx=%d)", q3Idx, a3Idx)
	}

	// Log actual order for debugging
	t.Logf("Result messages (%d total):", len(result))
	for i, msg := range result {
		t.Logf("  [%d] %v: %s", i, msg.Role, msg.Content[:min(len(msg.Content), 30)])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestContextRewriter_NonAdjacentPairs verifies tool pairs with messages between them
func TestContextRewriter_NonAdjacentPairs(t *testing.T) {
	ctx := context.Background()

	// Scenario: Assistant makes tool call, user sends another message before tool result
	// This is an edge case that shouldn't normally happen, but we should handle it
	input := []*schema.Message{
		{Role: schema.System, Content: "System"},
		{Role: schema.User, Content: "Search for code"},
		{
			Role:    schema.Assistant,
			Content: "",
			ToolCalls: []schema.ToolCall{
				{ID: "call-1", Function: schema.FunctionCall{Name: "search_code"}},
			},
		},
		{Role: schema.Tool, Content: "Results here", Name: "search_code", ToolCallID: "call-1"},
	}

	rewriter := NewContextRewriter(10000)
	result := rewriter(ctx, input)

	// Should keep all messages and maintain the relationship
	if len(result) != 4 {
		t.Errorf("NonAdjacentPairs: got %d messages, want 4", len(result))
	}

	// Verify assistant with tool calls and its result are both present
	hasAssistantWithTools := false
	hasToolResult := false
	for _, msg := range result {
		if msg.Role == schema.Assistant && len(msg.ToolCalls) > 0 {
			hasAssistantWithTools = true
		}
		if msg.Role == schema.Tool && msg.ToolCallID == "call-1" {
			hasToolResult = true
		}
	}

	if !hasAssistantWithTools {
		t.Error("NonAdjacentPairs: missing assistant with tool calls")
	}
	if !hasToolResult {
		t.Error("NonAdjacentPairs: missing tool result")
	}
}

// TestContextRewriter_ToolCallIDMatching verifies matching by ToolCallID
func TestContextRewriter_ToolCallIDMatching(t *testing.T) {
	ctx := context.Background()

	input := []*schema.Message{
		{Role: schema.System, Content: "System"},
		{Role: schema.User, Content: "Question"},
		{
			Role:    schema.Assistant,
			Content: "",
			ToolCalls: []schema.ToolCall{
				{ID: "specific-id-123", Function: schema.FunctionCall{Name: "search_code"}},
			},
		},
		{Role: schema.Tool, Content: "Results", Name: "search_code", ToolCallID: "specific-id-123"},
	}

	rewriter := NewContextRewriter(10000)
	result := rewriter(ctx, input)

	// Find the tool message and verify it has the correct ToolCallID
	for _, msg := range result {
		if msg.Role == schema.Tool {
			if msg.ToolCallID != "specific-id-123" {
				t.Errorf("ToolCallIDMatching: ToolCallID = %v, want specific-id-123", msg.ToolCallID)
			}
		}
	}
}

// TestContextRewriter_RealTokenBudgetTripsCompression is the regression guard for
// the max_context_size under-count: a transcript sized in the window where the old
// chars/4 estimate would pass (<= 4*limit chars) but the real token budget
// (~2.5 chars/token, minus headroom) is exceeded MUST compress. Before the fix the
// guard read chars/4 and shipped the context untouched; now it trips.
func TestContextRewriter_RealTokenBudgetTripsCompression(t *testing.T) {
	ctx := context.Background()

	a1 := strings.Repeat("a", 140)
	a2 := strings.Repeat("b", 140)
	input := []*schema.Message{
		{Role: schema.System, Content: "S"},
		{Role: schema.User, Content: "Q1"},
		{Role: schema.Assistant, Content: a1},
		{Role: schema.User, Content: "Q2"},
		{Role: schema.Assistant, Content: a2},
		{Role: schema.User, Content: "Q3"},
	}
	// total msgChars = 1 + 2 + 140 + 2 + 140 + 2 = 287.
	// old chars/4 budget at limit 100 = 400 chars -> 287 <= 400 -> NO compression (the bug).
	// new budget = 100 * (1-0.10) * 2.5 = 225 chars -> 287 > 225 -> compression.
	rewriter := NewContextRewriter(100)
	result := rewriter(ctx, input)

	if len(result) >= len(input) {
		t.Fatalf("expected compression in the real-token window, got %d messages (input %d)",
			len(result), len(input))
	}
	// All user turns survive; the most recent assistant survives; the oldest is dropped.
	if countByRole(result, schema.User) != 3 {
		t.Errorf("all 3 user turns must be preserved, got %d", countByRole(result, schema.User))
	}
	if !hasContent(result, a2) {
		t.Error("most recent assistant message should be kept")
	}
	if hasContent(result, a1) {
		t.Error("oldest assistant message should have been dropped to fit the real-token budget")
	}
}

// TestContextRewriter_HardCeilingDropsOldestUserTurns verifies the hard ceiling:
// when the user messages alone exceed the budget, the oldest user turns are evicted
// (not warn-and-proceed-over-limit) while the most recent ones are kept in order.
func TestContextRewriter_HardCeilingDropsOldestUserTurns(t *testing.T) {
	ctx := context.Background()

	pad := strings.Repeat("x", 100)
	input := []*schema.Message{
		{Role: schema.User, Content: "U1-" + pad},
		{Role: schema.User, Content: "U2-" + pad},
		{Role: schema.User, Content: "U3-" + pad},
		{Role: schema.User, Content: "U4-" + pad},
	}
	// each user ~103 chars; budget = 100*(0.90)*2.5 = 225 chars -> only the two
	// newest (~206) fit; U1 and U2 evicted.
	rewriter := NewContextRewriter(100)
	result := rewriter(ctx, input)

	if hasContent(result, "U1-") || hasContent(result, "U2-") {
		t.Error("oldest user turns U1/U2 should have been evicted by the hard ceiling")
	}
	if !hasContent(result, "U3-") || !hasContent(result, "U4-") {
		t.Error("most recent user turns U3/U4 should be kept")
	}
	total := estimateMessagesChars(result)
	if total > 225 {
		t.Errorf("kept context %d chars exceeds the hard ceiling of 225", total)
	}
}

// TestContextRewriter_HardCeilingKeepsLiveTurn verifies the floor: the most recent
// user message (the live turn) is never dropped, even if it alone exceeds the
// budget — we send it over-limit rather than emptying the request.
func TestContextRewriter_HardCeilingKeepsLiveTurn(t *testing.T) {
	ctx := context.Background()

	huge := strings.Repeat("z", 500)
	input := []*schema.Message{
		{Role: schema.User, Content: "old-" + strings.Repeat("y", 200)},
		{Role: schema.User, Content: "live-" + huge},
	}
	rewriter := NewContextRewriter(10) // tiny budget: 10*0.9*2.5 = 22 chars
	result := rewriter(ctx, input)

	if !hasContent(result, "live-") {
		t.Error("the live (most recent) user turn must always be kept")
	}
	if hasContent(result, "old-") {
		t.Error("the older oversized user turn should be evicted")
	}
}

// TestContextRewriter_CalibratorTightensBudget verifies the rewriter consults the
// calibrator: a context that fits under the default ratio must compress once a real
// low-ratio sample (more tokens per char) is recorded.
func TestContextRewriter_CalibratorTightensBudget(t *testing.T) {
	ctx := context.Background()

	body := strings.Repeat("c", 200)
	input := []*schema.Message{
		{Role: schema.System, Content: "S"},
		{Role: schema.User, Content: "Q1"},
		{Role: schema.Assistant, Content: body},
		{Role: schema.User, Content: "Q2"},
	}
	// msgChars = 1 + 2 + 200 + 2 = 205.
	// default ratio 2.5: budget = 100*0.9*2.5 = 225 -> 205 <= 225 -> no compression.
	cal := NewTokenCalibrator()
	cfg := ContextRewriterConfig{MaxContextTokens: 100, Calibrator: cal}
	rewriter := NewContextRewriterFromConfig(cfg)

	if got := rewriter(ctx, input); len(got) != len(input) {
		t.Fatalf("with default ratio context should fit; got %d of %d", len(got), len(input))
	}
	// Record a real sample: this request was ~206 chars but the provider counted
	// 150 prompt_tokens -> ratio ~1.37, clamped to 1.5. budget = 100*0.9*1.5 = 135.
	cal.RecordRequestChars(206)
	cal.RecordPromptTokens(150)
	got := rewriter(ctx, input)
	if len(got) >= len(input) {
		t.Errorf("calibrated low ratio should tighten the budget and force compression, got %d of %d",
			len(got), len(input))
	}
}

// TestContextRewriter_ParallelToolCallsAtomic guards against a malformed transcript
// under compression: an assistant that issued several tool calls in one step must be
// kept together with ALL of its tool results, or dropped entirely. Keeping the
// assistant while evicting one of its tool results leaves a tool_call with no
// matching result — which OpenAI-strict providers reject with a 400.
func TestContextRewriter_ParallelToolCallsAtomic(t *testing.T) {
	ctx := context.Background()

	big := strings.Repeat("r", 50)
	input := []*schema.Message{
		{Role: schema.System, Content: "S"},
		{Role: schema.User, Content: "Q"},
		{
			Role:    schema.Assistant,
			Content: "calling two tools",
			ToolCalls: []schema.ToolCall{
				{ID: "c1", Type: "function", Function: schema.FunctionCall{Name: "t1", Arguments: "{}"}},
				{ID: "c2", Type: "function", Function: schema.FunctionCall{Name: "t2", Arguments: "{}"}},
			},
		},
		{Role: schema.Tool, Content: big, Name: "t1", ToolCallID: "c1"},
		{Role: schema.Tool, Content: big, Name: "t2", ToolCallID: "c2"},
		{Role: schema.Assistant, Content: "final answer"},
	}
	// Budget tuned so assistant+ONE tool result fits but assistant+BOTH does not —
	// the exact window where one-at-a-time pairing would orphan a tool_call.
	rewriter := NewContextRewriter(60) // maxChars = 60 * 0.9 * 2.5 = 135
	result := rewriter(ctx, input)

	keptToolIDs := map[string]bool{}
	for _, m := range result {
		if m.Role == schema.Tool {
			keptToolIDs[m.ToolCallID] = true
		}
	}
	for _, m := range result {
		if m.Role == schema.Assistant {
			for _, tc := range m.ToolCalls {
				if !keptToolIDs[tc.ID] {
					t.Errorf("kept assistant has tool_call %q with no matching tool result (malformed transcript)", tc.ID)
				}
			}
		}
	}
}

// Helper function to count messages by role
func countByRole(messages []*schema.Message, role schema.RoleType) int {
	count := 0
	for _, msg := range messages {
		if msg.Role == role {
			count++
		}
	}
	return count
}

// Helper function to check if content exists in messages
func hasContent(messages []*schema.Message, content string) bool {
	for _, msg := range messages {
		if strings.Contains(msg.Content, content) {
			return true
		}
	}
	return false
}

// TestContextRewriter_ToolCallsCountedInSize verifies that ToolCalls are included in size calculation
func TestContextRewriter_ToolCallsCountedInSize(t *testing.T) {
	ctx := context.Background()

	// Create a message with large ToolCall arguments (simulating real tool calls)
	largeArgs := strings.Repeat("x", 2000) // 2000 chars in arguments
	input := []*schema.Message{
		{Role: schema.System, Content: "S"},
		{Role: schema.User, Content: "Q"},
		{
			Role:    schema.Assistant,
			Content: "Let me search", // Small content
			ToolCalls: []schema.ToolCall{
				{
					ID:   "call-1",
					Type: "function",
					Function: schema.FunctionCall{
						Name:      "search_code",
						Arguments: `{"query":"` + largeArgs + `"}`, // Large JSON payload
					},
				},
			},
		},
		{Role: schema.Tool, Content: "Results here", Name: "search_code", ToolCallID: "call-1"},
		{Role: schema.Assistant, Content: "Final answer"},
	}

	// Use a limit that would fit if we only count Content, but NOT if we count ToolCalls
	// System: 1, User: 1, Assistant content: ~15, Tool: ~12, Final: ~12 = ~41 chars
	// But ToolCall adds: ID(6) + Type(8) + Name(11) + Arguments(~2020) = ~2045 chars
	// Total: ~2086 chars = ~521 tokens
	rewriter := NewContextRewriter(100) // 100 tokens = 400 chars (tight)

	result := rewriter(ctx, input)

	// With proper ToolCall counting, the assistant+tool pair should be dropped
	// Result should have: System, User, Final answer (most recent assistant)
	hasToolCallAssistant := false
	hasToolResult := false
	for _, msg := range result {
		if msg.Role == schema.Assistant && len(msg.ToolCalls) > 0 {
			hasToolCallAssistant = true
		}
		if msg.Role == schema.Tool {
			hasToolResult = true
		}
	}

	// The pair should be dropped because ToolCall arguments are counted
	if hasToolCallAssistant {
		t.Error("ToolCallsCountedInSize: assistant with large ToolCall should have been removed")
	}
	if hasToolResult {
		t.Error("ToolCallsCountedInSize: tool result should have been removed (pair dropped)")
	}

	// User and final answer should remain
	hasUser := false
	hasFinalAnswer := false
	for _, msg := range result {
		if msg.Role == schema.User {
			hasUser = true
		}
		if msg.Role == schema.Assistant && strings.Contains(msg.Content, "Final") {
			hasFinalAnswer = true
		}
	}
	if !hasUser {
		t.Error("ToolCallsCountedInSize: user message should be preserved")
	}
	if !hasFinalAnswer {
		t.Error("ToolCallsCountedInSize: final assistant answer should be preserved")
	}
}

// TestContextRewriter_AccurateTokenCounting verifies size calculation includes all message parts
func TestContextRewriter_AccurateTokenCounting(t *testing.T) {
	// Test that messageChars counts all parts
	testMsg := &schema.Message{
		Role:    schema.Assistant,
		Content: "abc", // 3 chars
		ToolCalls: []schema.ToolCall{
			{
				ID:   "12345",    // 5 chars
				Type: "function", // 8 chars
				Function: schema.FunctionCall{
					Name:      "tool_name", // 9 chars
					Arguments: `{"key":"value"}`, // 15 chars
				},
			},
		},
	}

	// Total: 3 + 5 + 8 + 9 + 15 = 40 chars
	expectedSize := 40
	actualSize := messageChars(testMsg)

	if actualSize != expectedSize {
		t.Errorf("AccurateTokenCounting: messageChars = %d, want %d", actualSize, expectedSize)
	}

	// Test tool message
	toolMsg := &schema.Message{
		Role:       schema.Tool,
		Content:    "result", // 6 chars
		Name:       "search_code", // 11 chars
		ToolCallID: "call-123", // 8 chars
	}

	// Total: 6 + 11 + 8 = 25 chars
	expectedToolSize := 25
	actualToolSize := messageChars(toolMsg)

	if actualToolSize != expectedToolSize {
		t.Errorf("AccurateTokenCounting: messageChars(tool) = %d, want %d", actualToolSize, expectedToolSize)
	}
}
