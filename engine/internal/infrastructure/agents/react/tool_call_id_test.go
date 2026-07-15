package react

import (
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
)

// Some OpenAI-compatible providers stream tool calls with an empty id. The
// OpenAI request contract requires assistant.tool_calls[].id and the matching
// tool message tool_call_id to be present — strict providers reject the
// follow-up request ("missing field tool_call_id") and the turn dies after a
// successful tool run. normalizeToolCallIDs backfills stable ids on the
// concatenated assistant message BEFORE it is recorded and fed to the
// ToolsNode, so both sides get the same non-empty id.
func TestNormalizeToolCallIDs_BackfillsEmptyIDs(t *testing.T) {
	msg := &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{
			{Function: schema.FunctionCall{Name: "spawn_dchild", Arguments: `{"action":"spawn"}`}},
			{Function: schema.FunctionCall{Name: "memory_recall", Arguments: `{"query":"x"}`}},
		},
	}

	normalizeToolCallIDs(msg, "n0nce1", 3)

	assert.Equal(t, "call-n0nce1-s3-0-spawn_dchild", msg.ToolCalls[0].ID)
	assert.Equal(t, "call-n0nce1-s3-1-memory_recall", msg.ToolCalls[1].ID)
	assert.NotEqual(t, msg.ToolCalls[0].ID, msg.ToolCalls[1].ID, "ids must be unique within the message")
}

func TestNormalizeToolCallIDs_UniqueAcrossTurns(t *testing.T) {
	// Two turns of one session both reset step to 0 — the per-turn nonce must
	// keep the persisted history collision-free.
	turn1 := &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{Function: schema.FunctionCall{Name: "tool_a"}}}}
	turn2 := &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{Function: schema.FunctionCall{Name: "tool_a"}}}}

	normalizeToolCallIDs(turn1, "aaaa1111", 0)
	normalizeToolCallIDs(turn2, "bbbb2222", 0)

	assert.NotEqual(t, turn1.ToolCalls[0].ID, turn2.ToolCalls[0].ID)
}

func TestNormalizeToolCallIDs_KeepsProviderIDs(t *testing.T) {
	msg := &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{
			{ID: "call_real", Function: schema.FunctionCall{Name: "tool_a"}},
			{Function: schema.FunctionCall{Name: "tool_b"}}, // only this one is empty
		},
	}

	normalizeToolCallIDs(msg, "n", 0)

	assert.Equal(t, "call_real", msg.ToolCalls[0].ID, "provider-issued ids must never be rewritten")
	assert.Equal(t, "call-n-s0-1-tool_b", msg.ToolCalls[1].ID)
}

func TestNormalizeToolCallIDs_SanitizesModelControlledName(t *testing.T) {
	msg := &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{
			{Function: schema.FunctionCall{Name: `evil tool">\n{inject}` + string(make([]byte, 100))}},
		},
	}

	normalizeToolCallIDs(msg, "n", 0)

	assert.Equal(t, "call-n-s0-0-eviltoolninject", msg.ToolCalls[0].ID,
		"name must be reduced to [A-Za-z0-9_-] and truncated")
}

func TestNormalizeToolCallIDs_NilAndEmptySafe(t *testing.T) {
	normalizeToolCallIDs(nil, "n", 0) // must not panic
	msg := &schema.Message{Role: schema.Assistant}
	normalizeToolCallIDs(msg, "n", 0)
	assert.Empty(t, msg.ToolCalls)
}
