package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
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
	maxQuestions      = 10
	maxQuestionOpts   = 5
	questionTypeText  = "text"
	questionTypeSel   = "select"
	questionTypeMulti = "multiselect"
)

// supportedOutputTypes is the closed set accepted by the tool. Anything else
// returns a tool error rather than silently emitting a degenerate widget.
var supportedOutputTypes = map[string]struct{}{
	"summary_table": {},
	"form":          {},
	"info":          {},
}

// decodeStrict decodes into T with DisallowUnknownFields. Used everywhere in
// the structured_output parsing pipeline so invented fields fail loud instead
// of being silently dropped by the default permissive json.Unmarshal.
func decodeStrict[T any](data []byte) (T, error) {
	var t T
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return t, err
	}
	return t, nil
}

func (t *StructuredOutputTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "show_structured_output",
		Desc: `Emit a structured data block to the user and HALT this turn. The user's reply (for form mode) arrives as a resume on the next turn.

STRICT INPUT CONTRACT — invalid args return "[ERROR] …" to you. Unknown fields and unknown output_type values are rejected, not silently dropped.

"output_type" (required) MUST be exactly one of:
  - "summary_table" — rows + optional action buttons
  - "form"          — 1 to 10 input fields ("questions")
  - "info"          — title/description only, no input

Common fields:
  - "title"       (optional, string)  — block title
  - "description" (optional, string)  — description text
  - "rows"        (summary_table)     — JSON array of {"label","value"}
  - "actions"     (summary_table)     — JSON array of {"label","type","value"}, type ∈ "primary"|"secondary"
  - "questions"   (form)              — JSON array of question objects

Question object:
  - "id"      (string, required for multi-question forms; auto-generated server-side when only one question and id is omitted)
  - "label"   (string, required)      — prompt text shown to user
  - "type"    (string, required)      — exactly one of "text" | "select" | "multiselect"
  - "options" (array, required for select/multiselect, 2-5 entries) — JSON array of {"label","value"}; "value" defaults to "label" when omitted
  - "default" (string, optional)      — pre-fill value

Examples:

  summary_table with action buttons:
    {
      "output_type": "summary_table",
      "title": "Confirm deletion",
      "rows": [{"label":"target","value":"prod-db"}],
      "actions": [
        {"label":"Delete","type":"primary","value":"delete"},
        {"label":"Cancel","type":"secondary","value":"cancel"}
      ]
    }

  form with a single question (id may be omitted — server generates one):
    {
      "output_type": "form",
      "questions": [
        {"label":"Region?","type":"select","options":[{"label":"EU"},{"label":"US"}]}
      ]
    }

  form with multi-question wizard (every id required):
    {
      "output_type": "form",
      "questions": [
        {"id":"name","label":"Device name","type":"text"},
        {"id":"platform","label":"Platform","type":"select","options":[{"label":"iOS"},{"label":"Android"}]}
      ]
    }

  info (no input collected):
    {"output_type":"info","title":"Deployment status","description":"Rolling restart in progress."}

ON TOOL ERROR — STOP. Do NOT retry the same args; surface the error to the user or escalate. Re-reading this tool description before retrying is the right path when the error is about field shape.
`,
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"output_type": {Type: schema.String, Desc: `Exactly one of: "summary_table" | "form" | "info"`, Required: true},
			"title":       {Type: schema.String, Desc: "Title of the output block"},
			"description": {Type: schema.String, Desc: "Description text"},
			"rows":        {Type: schema.String, Desc: `JSON array of rows: [{"label":"Name","value":"MyProject"}]. Stringified arrays accepted.`},
			"actions":     {Type: schema.String, Desc: `JSON array of actions: [{"label":"Deploy","type":"primary","value":"deploy"}]. Stringified arrays accepted.`},
			"questions":   {Type: schema.String, Desc: `JSON array of form inputs: [{"id":"platform","label":"Platform?","type":"select","options":[{"label":"iOS"},{"label":"Android"}]}]. Up to 10 questions. Stringified arrays accepted at any depth.`},
		}),
	}, nil
}

func (t *StructuredOutputTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	args, err := decodeStrict[structuredOutputArgs]([]byte(argumentsInJSON))
	if err != nil {
		return fmt.Sprintf("[ERROR] %v", err), nil
	}

	if args.OutputType == "" {
		return "[ERROR] output_type is required", nil
	}
	if _, ok := supportedOutputTypes[args.OutputType]; !ok {
		return fmt.Sprintf(`[ERROR] unknown output_type %q. Supported: summary_table | form | info`, args.OutputType), nil
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

	// Server-issued interrupt_id correlates the widget with resume_interrupt
	// and is the PK in the interrupts state-tracker table. Generated before
	// validation so the auto-id branch can derive a synthetic question.id from
	// it when the model omits id on a single-question form.
	interruptID := uuid.NewString()

	if validationErr := validateStructuredQuestions(args.OutputType, questions, interruptID); validationErr != "" {
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

	// The loop halts via the owned-loop return-directly route keyed on this tool's
	// HITL name (see react.ownedReturnDirectlyMap): once the widget fires the turn
	// ends and the user's resume_interrupt POST drives the next one. The tool just
	// emits the widget and returns — it does not touch loop state.
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
		rows, err := decodeStrict[[]domain.StructuredRow]([]byte(asString))
		if err != nil {
			return nil, fmt.Errorf("failed to parse rows string: %w", err)
		}
		return rows, nil
	}

	// Fallback: direct array.
	rows, err := decodeStrict[[]domain.StructuredRow](raw)
	if err != nil {
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
		actions, err := decodeStrict[[]domain.StructuredAction]([]byte(asString))
		if err != nil {
			return nil, fmt.Errorf("failed to parse actions string: %w", err)
		}
		return actions, nil
	}

	actions, err := decodeStrict[[]domain.StructuredAction](raw)
	if err != nil {
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
	// Each Question decodes via its custom UnmarshalJSON which itself applies
	// DisallowUnknownFields and the same string-or-array fallback to `options`.
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		if asString == "" {
			return nil, nil
		}
		qs, err := decodeStrict[[]domain.Question]([]byte(asString))
		if err != nil {
			return nil, fmt.Errorf("failed to parse questions string: %w", err)
		}
		return qs, nil
	}

	// Fallback: direct array.
	qs, err := decodeStrict[[]domain.Question](raw)
	if err != nil {
		return nil, fmt.Errorf("failed to parse questions: %w", err)
	}
	return qs, nil
}

// validateStructuredQuestions returns "" when questions are valid for the given
// output_type, else a short error message. interruptID seeds the synthetic id
// applied to single-question forms whose model omitted `id` — multi-question
// forms still require explicit ids because the model picks meaningful keys
// that downstream code may correlate by.
func validateStructuredQuestions(outputType string, questions []domain.Question, interruptID string) string {
	if outputType == "form" && len(questions) == 0 {
		return "form output_type requires at least one question"
	}
	if len(questions) > maxQuestions {
		return fmt.Sprintf("too many questions: maximum %d allowed", maxQuestions)
	}

	// Auto-id on single-question forms — the mapping back to the question is
	// unambiguous from interrupt_id, so requiring the model to invent an id
	// for a single answer is busywork that smaller models routinely fill
	// with throwaway strings like "q1" or "answer".
	if len(questions) == 1 && questions[0].ID == "" && len(interruptID) >= 8 {
		questions[0].ID = "q-" + interruptID[:8]
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
