package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// mockEventEmitter records emitted events for testing
type mockEventEmitter struct {
	events []*domain.AgentEvent
}

func (m *mockEventEmitter) Send(event *domain.AgentEvent) error {
	m.events = append(m.events, event)
	return nil
}

func TestStructuredOutput_SummaryTable(t *testing.T) {
	emitter := &mockEventEmitter{}
	tool := NewStructuredOutputTool(emitter, "sess-1")

	args := `{
		"output_type": "summary_table",
		"title": "Project Overview",
		"description": "Current project configuration",
		"rows": [
			{"label": "Name", "value": "MyProject"},
			{"label": "Language", "value": "Go"},
			{"label": "Version", "value": "1.24"}
		]
	}`

	result, err := tool.InvokableRun(context.Background(), args)
	require.NoError(t, err)
	assert.Equal(t, "Structured output displayed to user.", result)

	// Verify event was emitted as the HITL Interrupt Primitive (engine 1.2.0+).
	require.Len(t, emitter.events, 1)
	event := emitter.events[0]
	assert.Equal(t, domain.EventTypeInterruptRequest, event.Type)
	assert.NotEmpty(t, event.Metadata["interrupt_id"], "interrupt_id required for client correlation")
	assert.Equal(t, string(domain.InterruptKindStructuredOutput), event.Metadata["kind"])

	// Decode the wrapped payload, then the schema body inside it.
	var payload domain.InterruptRequestPayload
	require.NoError(t, json.Unmarshal([]byte(event.Content), &payload))
	assert.Equal(t, domain.InterruptKindStructuredOutput, payload.Kind)
	assert.NotEmpty(t, payload.InterruptID)

	var output domain.StructuredOutput
	require.NoError(t, json.Unmarshal(payload.Schema, &output))
	assert.Equal(t, "summary_table", output.OutputType)
	assert.Equal(t, "Project Overview", output.Title)
	assert.Equal(t, "Current project configuration", output.Description)
	require.Len(t, output.Rows, 3)
	assert.Equal(t, "Name", output.Rows[0].Label)
	assert.Equal(t, "MyProject", output.Rows[0].Value)
	assert.Equal(t, "Language", output.Rows[1].Label)
	assert.Equal(t, "Go", output.Rows[1].Value)
	assert.Equal(t, "Version", output.Rows[2].Label)
	assert.Equal(t, "1.24", output.Rows[2].Value)
}

func TestStructuredOutput_WithActions(t *testing.T) {
	emitter := &mockEventEmitter{}
	tool := NewStructuredOutputTool(emitter, "sess-1")

	args := `{
		"output_type": "summary_table",
		"title": "Deployment Ready",
		"rows": [{"label": "Status", "value": "Ready"}],
		"actions": [
			{"label": "Deploy Now", "type": "primary", "value": "deploy"},
			{"label": "Cancel", "type": "secondary", "value": "cancel"}
		]
	}`

	result, err := tool.InvokableRun(context.Background(), args)
	require.NoError(t, err)
	assert.Equal(t, "Structured output displayed to user.", result)

	require.Len(t, emitter.events, 1)
	event := emitter.events[0]

	var payload domain.InterruptRequestPayload
	require.NoError(t, json.Unmarshal([]byte(event.Content), &payload))
	var output domain.StructuredOutput
	require.NoError(t, json.Unmarshal(payload.Schema, &output))
	assert.Equal(t, "summary_table", output.OutputType)
	assert.Equal(t, "Deployment Ready", output.Title)
	require.Len(t, output.Actions, 2)
	assert.Equal(t, "Deploy Now", output.Actions[0].Label)
	assert.Equal(t, "primary", output.Actions[0].Type)
	assert.Equal(t, "deploy", output.Actions[0].Value)
	assert.Equal(t, "Cancel", output.Actions[1].Label)
	assert.Equal(t, "secondary", output.Actions[1].Type)
	assert.Equal(t, "cancel", output.Actions[1].Value)
}

func TestStructuredOutput_MissingOutputType(t *testing.T) {
	emitter := &mockEventEmitter{}
	tool := NewStructuredOutputTool(emitter, "sess-1")

	args := `{"title": "No Type"}`
	result, err := tool.InvokableRun(context.Background(), args)
	require.NoError(t, err)
	assert.Contains(t, result, "[ERROR]")
	assert.Contains(t, result, "output_type is required")
	assert.Empty(t, emitter.events)
}

func TestStructuredOutput_InvalidJSON(t *testing.T) {
	emitter := &mockEventEmitter{}
	tool := NewStructuredOutputTool(emitter, "sess-1")

	result, err := tool.InvokableRun(context.Background(), `{invalid}`)
	require.NoError(t, err)
	assert.Contains(t, result, "[ERROR]")
	assert.Empty(t, emitter.events)
}

func TestStructuredOutput_NilEmitter(t *testing.T) {
	// Should not panic when emitter is nil
	tool := NewStructuredOutputTool(nil, "sess-1")

	args := `{"output_type": "summary_table", "title": "Test"}`
	result, err := tool.InvokableRun(context.Background(), args)
	require.NoError(t, err)
	assert.Equal(t, "Structured output displayed to user.", result)
}

func TestStructuredOutput_ToolInfo(t *testing.T) {
	tool := NewStructuredOutputTool(nil, "sess-1")

	info, err := tool.Info(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "show_structured_output", info.Name)
	assert.Contains(t, info.Desc, "HALT")
	assert.Contains(t, info.Desc, "form")
	assert.Contains(t, info.Desc, "STRICT INPUT CONTRACT")
}

func TestStructuredOutput_FormMode(t *testing.T) {
	emitter := &mockEventEmitter{}
	tool := NewStructuredOutputTool(emitter, "sess-1")

	args := `{
		"output_type": "form",
		"title": "Configure project",
		"questions": [
			{"id": "platform", "label": "Target platform?", "type": "select", "options": [{"label": "iOS"}, {"label": "Android"}]},
			{"id": "name", "label": "Project name?", "type": "text"}
		]
	}`

	result, err := tool.InvokableRun(context.Background(), args)
	require.NoError(t, err)
	assert.Equal(t, "Structured output displayed to user.", result)

	require.Len(t, emitter.events, 1)
	event := emitter.events[0]
	assert.Equal(t, domain.EventTypeInterruptRequest, event.Type)

	var payload domain.InterruptRequestPayload
	require.NoError(t, json.Unmarshal([]byte(event.Content), &payload))
	var output domain.StructuredOutput
	require.NoError(t, json.Unmarshal(payload.Schema, &output))
	assert.Equal(t, "form", output.OutputType)
	require.Len(t, output.Questions, 2)
	assert.Equal(t, "platform", output.Questions[0].ID)
	assert.Equal(t, "select", output.Questions[0].Type)
	require.Len(t, output.Questions[0].Options, 2)
	assert.Equal(t, "iOS", output.Questions[0].Options[0].Label)
	assert.Equal(t, "name", output.Questions[1].ID)
	assert.Equal(t, "text", output.Questions[1].Type)
}

func TestStructuredOutput_FormMode_QuestionsAsJSONString(t *testing.T) {
	emitter := &mockEventEmitter{}
	tool := NewStructuredOutputTool(emitter, "sess-1")

	// LLMs typically emit schema.String params as JSON-encoded strings.
	args := `{
		"output_type": "form",
		"questions": "[{\"id\":\"q1\",\"label\":\"Why?\",\"type\":\"text\"}]"
	}`

	result, err := tool.InvokableRun(context.Background(), args)
	require.NoError(t, err)
	assert.Equal(t, "Structured output displayed to user.", result)

	require.Len(t, emitter.events, 1)
	var payload domain.InterruptRequestPayload
	require.NoError(t, json.Unmarshal([]byte(emitter.events[0].Content), &payload))
	var output domain.StructuredOutput
	require.NoError(t, json.Unmarshal(payload.Schema, &output))
	require.Len(t, output.Questions, 1)
	assert.Equal(t, "q1", output.Questions[0].ID)
}

func TestStructuredOutput_FormMode_RequiresQuestions(t *testing.T) {
	emitter := &mockEventEmitter{}
	tool := NewStructuredOutputTool(emitter, "sess-1")

	args := `{"output_type": "form", "title": "Empty"}`
	result, err := tool.InvokableRun(context.Background(), args)
	require.NoError(t, err)
	assert.Contains(t, result, "[ERROR]")
	assert.Contains(t, result, "form output_type requires at least one question")
	assert.Empty(t, emitter.events)
}

func TestStructuredOutput_FormMode_RejectsUnknownType(t *testing.T) {
	emitter := &mockEventEmitter{}
	tool := NewStructuredOutputTool(emitter, "sess-1")

	args := `{
		"output_type": "form",
		"questions": [{"id": "q1", "label": "?", "type": "slider"}]
	}`
	result, err := tool.InvokableRun(context.Background(), args)
	require.NoError(t, err)
	assert.Contains(t, result, "[ERROR]")
	assert.Contains(t, result, "unsupported type")
	assert.Empty(t, emitter.events)
}

func TestStructuredOutput_FormMode_RejectsTooManyQuestions(t *testing.T) {
	emitter := &mockEventEmitter{}
	tool := NewStructuredOutputTool(emitter, "sess-1")

	// 11 questions exceeds maxQuestions=10.
	args := `{
		"output_type": "form",
		"questions": [
			{"id":"q1","label":"a","type":"text"},
			{"id":"q2","label":"b","type":"text"},
			{"id":"q3","label":"c","type":"text"},
			{"id":"q4","label":"d","type":"text"},
			{"id":"q5","label":"e","type":"text"},
			{"id":"q6","label":"f","type":"text"},
			{"id":"q7","label":"g","type":"text"},
			{"id":"q8","label":"h","type":"text"},
			{"id":"q9","label":"i","type":"text"},
			{"id":"q10","label":"j","type":"text"},
			{"id":"q11","label":"k","type":"text"}
		]
	}`
	result, err := tool.InvokableRun(context.Background(), args)
	require.NoError(t, err)
	assert.Contains(t, result, "[ERROR]")
	assert.Contains(t, result, "too many questions")
	assert.Contains(t, result, "maximum 10")
	assert.Empty(t, emitter.events)
}

func TestStructuredOutput_FormMode_SelectRequiresMinTwoOptions(t *testing.T) {
	emitter := &mockEventEmitter{}
	tool := NewStructuredOutputTool(emitter, "sess-1")

	args := `{
		"output_type": "form",
		"questions": [{"id":"q1","label":"?","type":"select","options":[{"label":"only"}]}]
	}`
	result, err := tool.InvokableRun(context.Background(), args)
	require.NoError(t, err)
	assert.Contains(t, result, "[ERROR]")
	assert.Contains(t, result, "requires at least 2 options")
	assert.Empty(t, emitter.events)
}

// The tool's JSON Schema declares `rows`, `actions`, and `questions` as
// `schema.String` (with descriptions naming the inner array shape), so frontier
// LLMs — qwen3-coder-next, gpt-5-mini, etc. — frequently emit them as
// string-encoded JSON literals: `"rows": "[{...}]"`. The previous typed
// Unmarshal returned `cannot unmarshal string into Go struct field ...Rows`,
// which is non-retryable in the same shape, but model retry-loops on it
// regardless, burning the turn until max_turn_duration. The lenient parsers
// (parseStructuredRows, parseStructuredActions) restore the contract.
func TestStructuredOutput_StringEncodedRowsAndActions(t *testing.T) {
	emitter := &mockEventEmitter{}
	tool := NewStructuredOutputTool(emitter, "sess-1")

	// Both rows and actions emitted as string-encoded JSON (the failure mode).
	args := `{
		"output_type": "summary_table",
		"title": "Ready",
		"rows": "[{\"label\":\"Name\",\"value\":\"MyProject\"},{\"label\":\"Lang\",\"value\":\"Go\"}]",
		"actions": "[{\"label\":\"Deploy\",\"type\":\"primary\",\"value\":\"deploy\"}]"
	}`

	result, err := tool.InvokableRun(context.Background(), args)
	require.NoError(t, err)
	assert.Equal(t, "Structured output displayed to user.", result)

	require.Len(t, emitter.events, 1)
	var payload domain.InterruptRequestPayload
	require.NoError(t, json.Unmarshal([]byte(emitter.events[0].Content), &payload))
	var output domain.StructuredOutput
	require.NoError(t, json.Unmarshal(payload.Schema, &output))
	require.Len(t, output.Rows, 2)
	assert.Equal(t, "Name", output.Rows[0].Label)
	assert.Equal(t, "MyProject", output.Rows[0].Value)
	require.Len(t, output.Actions, 1)
	assert.Equal(t, "Deploy", output.Actions[0].Label)
	assert.Equal(t, "primary", output.Actions[0].Type)
	assert.Equal(t, "deploy", output.Actions[0].Value)
}

// The literal-array shape (what the struct used to require pre-fix) must still
// work — both paths converge.
func TestStructuredOutput_LiteralRowsAndActions(t *testing.T) {
	emitter := &mockEventEmitter{}
	tool := NewStructuredOutputTool(emitter, "sess-1")

	args := `{
		"output_type": "summary_table",
		"rows": [{"label":"Name","value":"MyProject"}],
		"actions": [{"label":"Deploy","type":"primary","value":"deploy"}]
	}`

	result, err := tool.InvokableRun(context.Background(), args)
	require.NoError(t, err)
	assert.Equal(t, "Structured output displayed to user.", result)

	require.Len(t, emitter.events, 1)
	var payload domain.InterruptRequestPayload
	require.NoError(t, json.Unmarshal([]byte(emitter.events[0].Content), &payload))
	var output domain.StructuredOutput
	require.NoError(t, json.Unmarshal(payload.Schema, &output))
	require.Len(t, output.Rows, 1)
	assert.Equal(t, "MyProject", output.Rows[0].Value)
	require.Len(t, output.Actions, 1)
	assert.Equal(t, "Deploy", output.Actions[0].Label)
}

// Empty stringified arrays (a common LLM pattern when the model hesitates)
// should be treated as absent, not as parse errors.
func TestStructuredOutput_EmptyStringEncodedRowsAndActions(t *testing.T) {
	emitter := &mockEventEmitter{}
	tool := NewStructuredOutputTool(emitter, "sess-1")

	args := `{
		"output_type": "info",
		"title": "No data",
		"rows": "",
		"actions": ""
	}`

	result, err := tool.InvokableRun(context.Background(), args)
	require.NoError(t, err)
	assert.Equal(t, "Structured output displayed to user.", result)

	require.Len(t, emitter.events, 1)
	var payload domain.InterruptRequestPayload
	require.NoError(t, json.Unmarshal([]byte(emitter.events[0].Content), &payload))
	var output domain.StructuredOutput
	require.NoError(t, json.Unmarshal(payload.Schema, &output))
	assert.Empty(t, output.Rows)
	assert.Empty(t, output.Actions)
}

// Malformed string-encoded JSON should produce a clear error, not silently
// drop the field.
func TestStructuredOutput_MalformedStringEncodedRows(t *testing.T) {
	emitter := &mockEventEmitter{}
	tool := NewStructuredOutputTool(emitter, "sess-1")

	args := `{
		"output_type": "summary_table",
		"rows": "[{not valid json}]"
	}`

	result, err := tool.InvokableRun(context.Background(), args)
	require.NoError(t, err)
	assert.Contains(t, result, "[ERROR]")
	assert.Contains(t, result, "rows")
	assert.Empty(t, emitter.events)
}

// 1.2.1 hardening — fail-loud on unknown top-level fields.
func TestStructuredOutput_RejectsUnknownTopLevelField(t *testing.T) {
	emitter := &mockEventEmitter{}
	tool := NewStructuredOutputTool(emitter, "sess-1")

	args := `{"output_type":"info","title":"x","unknown_key":"value"}`
	result, err := tool.InvokableRun(context.Background(), args)
	require.NoError(t, err)
	assert.Contains(t, result, "[ERROR]")
	assert.Contains(t, result, "unknown_key")
	assert.Empty(t, emitter.events)
}

// 1.2.1 hardening — fail-loud on output_type outside the closed set.
func TestStructuredOutput_RejectsUnknownOutputType(t *testing.T) {
	emitter := &mockEventEmitter{}
	tool := NewStructuredOutputTool(emitter, "sess-1")

	args := `{"output_type":"single_select","title":"Pick"}`
	result, err := tool.InvokableRun(context.Background(), args)
	require.NoError(t, err)
	assert.Contains(t, result, "[ERROR]")
	assert.Contains(t, result, "unknown output_type")
	assert.Contains(t, result, "single_select")
	assert.Empty(t, emitter.events)
}

// 1.2.1 hardening — recursive lenient parse extends PR #75 down into
// questions[i].options.
func TestStructuredOutput_NestedStringifiedOptions(t *testing.T) {
	emitter := &mockEventEmitter{}
	tool := NewStructuredOutputTool(emitter, "sess-1")

	args := `{
		"output_type": "form",
		"questions": [
			{
				"id": "region",
				"label": "Region?",
				"type": "select",
				"options": "[{\"label\":\"EU\"},{\"label\":\"US\"}]"
			}
		]
	}`
	result, err := tool.InvokableRun(context.Background(), args)
	require.NoError(t, err)
	assert.Equal(t, "Structured output displayed to user.", result)

	require.Len(t, emitter.events, 1)
	var payload domain.InterruptRequestPayload
	require.NoError(t, json.Unmarshal([]byte(emitter.events[0].Content), &payload))
	var output domain.StructuredOutput
	require.NoError(t, json.Unmarshal(payload.Schema, &output))
	require.Len(t, output.Questions, 1)
	require.Len(t, output.Questions[0].Options, 2)
	assert.Equal(t, "EU", output.Questions[0].Options[0].Label)
	assert.Equal(t, "US", output.Questions[0].Options[1].Label)
}

// 1.2.1 hardening — nested stringified options that fail to parse must
// surface, not silently disappear.
func TestStructuredOutput_MalformedNestedStringifiedOptions(t *testing.T) {
	emitter := &mockEventEmitter{}
	tool := NewStructuredOutputTool(emitter, "sess-1")

	args := `{
		"output_type": "form",
		"questions": [
			{"id":"region","label":"Region?","type":"select","options":"["}
		]
	}`
	result, err := tool.InvokableRun(context.Background(), args)
	require.NoError(t, err)
	assert.Contains(t, result, "[ERROR]")
	assert.Contains(t, result, "options")
	assert.Empty(t, emitter.events)
}

// 1.2.1 hardening — single-question form auto-generates id from interrupt_id
// when the model omits it. Multi-question forms still require explicit ids
// (covered by TestStructuredOutput_MultiQuestionStillRequiresID).
func TestStructuredOutput_AutoIDOnSingleQuestion(t *testing.T) {
	emitter := &mockEventEmitter{}
	tool := NewStructuredOutputTool(emitter, "sess-1")

	args := `{
		"output_type": "form",
		"questions": [
			{"label":"Region?","type":"select","options":[{"label":"EU"},{"label":"US"}]}
		]
	}`
	result, err := tool.InvokableRun(context.Background(), args)
	require.NoError(t, err)
	assert.Equal(t, "Structured output displayed to user.", result)

	require.Len(t, emitter.events, 1)
	var payload domain.InterruptRequestPayload
	require.NoError(t, json.Unmarshal([]byte(emitter.events[0].Content), &payload))
	var output domain.StructuredOutput
	require.NoError(t, json.Unmarshal(payload.Schema, &output))
	require.Len(t, output.Questions, 1)
	assert.Truef(t, strings.HasPrefix(output.Questions[0].ID, "q-"),
		"expected synthetic id with q- prefix, got %q", output.Questions[0].ID)
	// Synthetic id is derived from first 8 chars of the UUID-shaped interrupt_id.
	assert.Equalf(t, 10, len(output.Questions[0].ID),
		"expected id length 10 (q- + 8 hex chars), got %d in %q",
		len(output.Questions[0].ID), output.Questions[0].ID)
}

// 1.2.1 — multi-question forms still require explicit ids because the model
// picks meaningful keys for cross-answer correlation.
func TestStructuredOutput_MultiQuestionStillRequiresID(t *testing.T) {
	emitter := &mockEventEmitter{}
	tool := NewStructuredOutputTool(emitter, "sess-1")

	args := `{
		"output_type": "form",
		"questions": [
			{"id":"name","label":"Device name","type":"text"},
			{"label":"Region?","type":"select","options":[{"label":"EU"},{"label":"US"}]}
		]
	}`
	result, err := tool.InvokableRun(context.Background(), args)
	require.NoError(t, err)
	assert.Contains(t, result, "[ERROR]")
	assert.Contains(t, result, "id is required")
	assert.Empty(t, emitter.events)
}

// 1.2.1 — maxQuestions raised 5 → 10. Exactly 10 must pass.
func TestStructuredOutput_FormMode_AcceptsTenQuestions(t *testing.T) {
	emitter := &mockEventEmitter{}
	tool := NewStructuredOutputTool(emitter, "sess-1")

	args := `{
		"output_type": "form",
		"questions": [
			{"id":"q1","label":"a","type":"text"},
			{"id":"q2","label":"b","type":"text"},
			{"id":"q3","label":"c","type":"text"},
			{"id":"q4","label":"d","type":"text"},
			{"id":"q5","label":"e","type":"text"},
			{"id":"q6","label":"f","type":"text"},
			{"id":"q7","label":"g","type":"text"},
			{"id":"q8","label":"h","type":"text"},
			{"id":"q9","label":"i","type":"text"},
			{"id":"q10","label":"j","type":"text"}
		]
	}`
	result, err := tool.InvokableRun(context.Background(), args)
	require.NoError(t, err)
	assert.Equal(t, "Structured output displayed to user.", result)
	require.Len(t, emitter.events, 1)
}

// 1.2.1 hardening — unknown field inside a question object must fail (the
// DisallowUnknownFields rule applies through Question.UnmarshalJSON).
func TestStructuredOutput_RejectsUnknownQuestionField(t *testing.T) {
	emitter := &mockEventEmitter{}
	tool := NewStructuredOutputTool(emitter, "sess-1")

	args := `{
		"output_type": "form",
		"questions": [
			{"id":"name","label":"Name","type":"text","extra":"oops"}
		]
	}`
	result, err := tool.InvokableRun(context.Background(), args)
	require.NoError(t, err)
	assert.Contains(t, result, "[ERROR]")
	assert.Contains(t, result, "extra")
	assert.Empty(t, emitter.events)
}
