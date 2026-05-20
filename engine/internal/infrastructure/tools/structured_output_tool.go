package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"github.com/syntheticinc/bytebrew/engine/internal/domain"
)

// StructuredOutputTool emits a structured data block to the user (summary tables,
// action buttons, or form-mode input requests). It is **non-blocking**: the
// tool returns immediately after emitting the event, and the agent's turn ends.
// In form mode the user's reply arrives as a normal chat message in a later turn.
type StructuredOutputTool struct {
	emitter   ToolEventEmitter
	sessionID string
}

// NewStructuredOutputTool creates a show_structured_output tool.
func NewStructuredOutputTool(emitter ToolEventEmitter, sessionID string) tool.InvokableTool {
	return &StructuredOutputTool{emitter: emitter, sessionID: sessionID}
}

type structuredOutputArgs struct {
	OutputType  string          `json:"output_type"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	// Rows / Actions / Questions are declared as schema.String in the tool's
	// JSON Schema (a deliberate concession — LLMs frequently emit string-typed
	// schema fields for array values), so the unmarshalled form is delayed
	// until parseStructured{Rows,Actions,Questions} runs and tries both the
	// string-encoded JSON and the literal-array shapes.
	Rows      json.RawMessage `json:"rows,omitempty"`
	Actions   json.RawMessage `json:"actions,omitempty"`
	Questions json.RawMessage `json:"questions,omitempty"`
}

const (
	maxQuestions      = 5
	maxQuestionOpts   = 5
	questionTypeText  = "text"
	questionTypeSel   = "select"
	questionTypeMulti = "multiselect"
)

func (t *StructuredOutputTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "show_structured_output",
		Desc: `Emit a structured data block to the user. Non-blocking: the tool returns immediately and the agent's turn ends. The user's reply (for form mode) arrives as the next chat message.

Modes (via "output_type"):
- "summary_table" — present rows + optional action buttons
- "info" — title/description only
- "form" — collect 1-5 inputs from the user (replaces the legacy ask_user blocking pattern)

Common params:
- "output_type" (required): "summary_table" | "form" | "info"
- "title" (optional): block title
- "description" (optional): description text
- "rows" (optional): [{"label":"Name","value":"MyProject"}] — table rows
- "actions" (optional): [{"label":"Deploy","type":"primary","value":"deploy"}] — buttons (type: "primary"|"secondary")
- "questions" (form mode): [{"id":"platform","label":"Platform?","type":"select","options":[{"label":"iOS"},{"label":"Android"}]}]

Question fields:
- "id" (required): stable identifier, returned with the answer
- "label" (required): prompt text
- "type" (required): "text" | "select" | "multiselect"
- "options" (required for select/multiselect): 2-5 options with "label" (and optional "value")
- "default" (optional): default value
`,
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"output_type": {Type: schema.String, Desc: `"summary_table" | "form" | "info"`, Required: true},
			"title":       {Type: schema.String, Desc: "Title of the output block"},
			"description": {Type: schema.String, Desc: "Description text"},
			"rows":        {Type: schema.String, Desc: `JSON array of rows: [{"label":"Name","value":"MyProject"}]`},
			"actions":     {Type: schema.String, Desc: `JSON array of actions: [{"label":"Deploy","type":"primary","value":"deploy"}]`},
			"questions":   {Type: schema.String, Desc: `JSON array of form inputs: [{"id":"platform","label":"Platform?","type":"select","options":[{"label":"iOS"},{"label":"Android"}]}]`},
		}),
	}, nil
}

func (t *StructuredOutputTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	var args structuredOutputArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid JSON: %v", err), nil
	}

	if args.OutputType == "" {
		return "[ERROR] output_type is required", nil
	}

	rows, err := parseStructuredRows(args.Rows)
	if err != nil {
		return fmt.Sprintf("[ERROR] %s", err), nil
	}

	actions, err := parseStructuredActions(args.Actions)
	if err != nil {
		return fmt.Sprintf("[ERROR] %s", err), nil
	}

	questions, err := parseStructuredQuestions(args.Questions)
	if err != nil {
		return fmt.Sprintf("[ERROR] %s", err), nil
	}

	if validationErr := validateStructuredQuestions(args.OutputType, questions); validationErr != "" {
		return "[ERROR] " + validationErr, nil
	}

	output := domain.StructuredOutput{
		OutputType:  args.OutputType,
		Title:       args.Title,
		Description: args.Description,
		Rows:        rows,
		Actions:     actions,
		Questions:   questions,
	}

	schemaJSON, err := json.Marshal(output)
	if err != nil {
		return fmt.Sprintf("[ERROR] failed to serialize output: %v", err), nil
	}

	// Server-issued interrupt_id correlates the widget with resume_interrupt
	// and is the PK in the interrupts state-tracker table.
	interruptID := uuid.NewString()

	payload := domain.InterruptRequestPayload{
		InterruptID: interruptID,
		Kind:        domain.InterruptKindStructuredOutput,
		Schema:      schemaJSON,
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Sprintf("[ERROR] failed to serialize interrupt payload: %v", err), nil
	}

	slog.InfoContext(ctx, "[structured_output] emitting interrupt_request event",
		"interrupt_id", interruptID,
		"output_type", args.OutputType,
		"rows", len(args.Rows),
		"actions", len(args.Actions),
		"questions", len(questions))

	if t.emitter != nil {
		_ = t.emitter.Send(&domain.AgentEvent{
			Type:      domain.EventTypeInterruptRequest,
			Timestamp: time.Now(),
			Content:   string(payloadJSON),
			Metadata: map[string]interface{}{
				"interrupt_id": interruptID,
				"kind":         string(domain.InterruptKindStructuredOutput),
			},
		})
	}

	// Halt the react loop — user's resume_interrupt POST drives the next turn.
	if err := react.SetReturnDirectly(ctx); err != nil {
		slog.ErrorContext(ctx, "[structured_output] SetReturnDirectly failed — react loop may not halt", "error", err)
	}

	return "Structured output displayed to user.", nil
}

// parseStructuredRows accepts either a raw JSON array or a string-encoded JSON
// array. Mirrors parseStructuredQuestions — the tool's JSON Schema declares
// `rows` as a String (with description "JSON array of rows"), so frontier LLMs
// emit it as a stringified JSON literal (`"rows": "[{...}]"`) and fail at the
// downstream typed Unmarshal. Lenient parsing here is what makes the schema
// declaration consistent with the struct shape.
func parseStructuredRows(raw json.RawMessage) ([]domain.StructuredRow, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	// Try string-encoded JSON first (typical LLM output for schema.String fields).
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		if asString == "" {
			return nil, nil
		}
		var rows []domain.StructuredRow
		if err := json.Unmarshal([]byte(asString), &rows); err != nil {
			return nil, fmt.Errorf("failed to parse rows string: %w", err)
		}
		return rows, nil
	}

	// Fallback: direct array.
	var rows []domain.StructuredRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("failed to parse rows: %w", err)
	}
	return rows, nil
}

// parseStructuredActions accepts either a raw JSON array or a string-encoded
// JSON array. Same rationale as parseStructuredRows.
func parseStructuredActions(raw json.RawMessage) ([]domain.StructuredAction, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		if asString == "" {
			return nil, nil
		}
		var actions []domain.StructuredAction
		if err := json.Unmarshal([]byte(asString), &actions); err != nil {
			return nil, fmt.Errorf("failed to parse actions string: %w", err)
		}
		return actions, nil
	}

	var actions []domain.StructuredAction
	if err := json.Unmarshal(raw, &actions); err != nil {
		return nil, fmt.Errorf("failed to parse actions: %w", err)
	}
	return actions, nil
}

// parseStructuredQuestions accepts either a raw JSON array or a string-encoded
// JSON array (LLMs frequently emit string-typed schema fields), or empty.
func parseStructuredQuestions(raw json.RawMessage) ([]domain.Question, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	// Try string-encoded JSON first (typical LLM output for schema.String fields).
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		if asString == "" {
			return nil, nil
		}
		var qs []domain.Question
		if err := json.Unmarshal([]byte(asString), &qs); err != nil {
			return nil, fmt.Errorf("failed to parse questions string: %w", err)
		}
		return qs, nil
	}

	// Fallback: direct array.
	var qs []domain.Question
	if err := json.Unmarshal(raw, &qs); err != nil {
		return nil, fmt.Errorf("failed to parse questions: %w", err)
	}
	return qs, nil
}

// validateStructuredQuestions returns "" when questions are valid for the given
// output_type, else a short error message.
func validateStructuredQuestions(outputType string, questions []domain.Question) string {
	if outputType == "form" && len(questions) == 0 {
		return "form output_type requires at least one question"
	}
	if len(questions) > maxQuestions {
		return fmt.Sprintf("too many questions: maximum %d allowed", maxQuestions)
	}

	for i, q := range questions {
		if q.ID == "" {
			return fmt.Sprintf("question %d: id is required", i+1)
		}
		if q.Label == "" {
			return fmt.Sprintf("question %d: label is required", i+1)
		}
		switch q.Type {
		case questionTypeText:
			// no options expected
		case questionTypeSel, questionTypeMulti:
			if len(q.Options) < 2 {
				return fmt.Sprintf("question %d: %s requires at least 2 options", i+1, q.Type)
			}
			if len(q.Options) > maxQuestionOpts {
				return fmt.Sprintf("question %d: too many options, maximum %d", i+1, maxQuestionOpts)
			}
			for j, opt := range q.Options {
				if opt.Label == "" {
					return fmt.Sprintf("question %d option %d: label is required", i+1, j+1)
				}
			}
		default:
			return fmt.Sprintf("question %d: unsupported type %q (use text|select|multiselect)", i+1, q.Type)
		}
	}
	return ""
}
