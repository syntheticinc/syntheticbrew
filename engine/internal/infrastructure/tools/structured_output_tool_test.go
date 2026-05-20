package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntheticinc/bytebrew/engine/internal/domain"
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

	// Verify event was emitted
	require.Len(t, emitter.events, 1)
	event := emitter.events[0]
	assert.Equal(t, domain.EventTypeStructuredOutput, event.Type)

	// Verify event content is valid StructuredOutput JSON
	var output domain.StructuredOutput
	err = json.Unmarshal([]byte(event.Content), &output)
	require.NoError(t, err)
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

	var output domain.StructuredOutput
	err = json.Unmarshal([]byte(event.Content), &output)
	require.NoError(t, err)
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
	assert.Contains(t, info.Desc, "Non-blocking")
	assert.Contains(t, info.Desc, "form")
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
	assert.Equal(t, domain.EventTypeStructuredOutput, event.Type)

	var output domain.StructuredOutput
	require.NoError(t, json.Unmarshal([]byte(event.Content), &output))
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
	var output domain.StructuredOutput
	require.NoError(t, json.Unmarshal([]byte(emitter.events[0].Content), &output))
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

	// 6 questions exceeds maxQuestions=5.
	args := `{
		"output_type": "form",
		"questions": [
			{"id":"q1","label":"a","type":"text"},
			{"id":"q2","label":"b","type":"text"},
			{"id":"q3","label":"c","type":"text"},
			{"id":"q4","label":"d","type":"text"},
			{"id":"q5","label":"e","type":"text"},
			{"id":"q6","label":"f","type":"text"}
		]
	}`
	result, err := tool.InvokableRun(context.Background(), args)
	require.NoError(t, err)
	assert.Contains(t, result, "[ERROR]")
	assert.Contains(t, result, "too many questions")
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
	var output domain.StructuredOutput
	require.NoError(t, json.Unmarshal([]byte(emitter.events[0].Content), &output))
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
	var output domain.StructuredOutput
	require.NoError(t, json.Unmarshal([]byte(emitter.events[0].Content), &output))
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
	var output domain.StructuredOutput
	require.NoError(t, json.Unmarshal([]byte(emitter.events[0].Content), &output))
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
