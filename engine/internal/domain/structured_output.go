package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// StructuredOutput represents a structured data block displayed to the user.
// Used for summary tables, action buttons, and non-blocking user-input forms.
type StructuredOutput struct {
	OutputType  string             `json:"output_type"` // "summary_table", "form", "info"
	Title       string             `json:"title,omitempty"`
	Description string             `json:"description,omitempty"`
	Rows        []StructuredRow    `json:"rows,omitempty"`
	Actions     []StructuredAction `json:"actions,omitempty"`
	Questions   []Question         `json:"questions,omitempty"` // form-mode input fields
}

// StructuredRow represents a single label-value row in a structured output block.
type StructuredRow struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// StructuredAction represents an interactive action button in a structured output block.
type StructuredAction struct {
	Label string `json:"label"`
	Type  string `json:"type"`  // "primary", "secondary"
	Value string `json:"value"` // machine-readable value sent back
}

// Question represents a single form input field. Used when StructuredOutput
// is in form mode (output_type="form"). The tool that emits a form-mode
// StructuredOutput is non-blocking: the agent's turn ends after emission and
// the user's reply arrives as the next chat message.
type Question struct {
	ID      string           `json:"id"`                // stable identifier returned with the answer
	Label   string           `json:"label"`             // human-readable prompt text
	Type    string           `json:"type"`              // "text", "select", "multiselect"
	Options []QuestionOption `json:"options,omitempty"` // required for select/multiselect (2-5)
	Default string           `json:"default,omitempty"` // optional default value
}

// QuestionOption represents a selectable option for a select/multiselect Question.
type QuestionOption struct {
	Label string `json:"label"`           // display label
	Value string `json:"value,omitempty"` // machine-readable value (defaults to Label if empty)
}

// UnmarshalJSON rejects unknown top-level fields and accepts `options` either
// as a JSON array literal or a string-encoded JSON array — matching the
// lenient-parse behaviour already in place at the top-level
// rows/actions/questions fields. Sub-frontier LLMs routinely emit
// `"options": "[{...}]"` instead of an array, and routinely invent extra keys.
// Both are surfaced as errors rather than silently dropped.
func (q *Question) UnmarshalJSON(data []byte) error {
	type rawQuestion struct {
		ID      string          `json:"id"`
		Label   string          `json:"label"`
		Type    string          `json:"type"`
		Options json.RawMessage `json:"options,omitempty"`
		Default string          `json:"default,omitempty"`
	}

	var r rawQuestion
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return err
	}

	q.ID = r.ID
	q.Label = r.Label
	q.Type = r.Type
	q.Default = r.Default
	q.Options = nil

	if len(r.Options) == 0 || string(r.Options) == "null" {
		return nil
	}

	// Stringified array first (typical sub-frontier emit), literal fallback.
	var asString string
	if err := json.Unmarshal(r.Options, &asString); err == nil {
		if asString == "" {
			return nil
		}
		opts, err := decodeQuestionOptionsStrict([]byte(asString))
		if err != nil {
			return fmt.Errorf("question options: failed to parse string: %w", err)
		}
		q.Options = opts
		return nil
	}

	opts, err := decodeQuestionOptionsStrict(r.Options)
	if err != nil {
		return fmt.Errorf("question options: %w", err)
	}
	q.Options = opts
	return nil
}

// decodeQuestionOptionsStrict decodes a []QuestionOption with
// DisallowUnknownFields so invented keys inside individual options surface as
// errors instead of silently dropping.
func decodeQuestionOptionsStrict(data []byte) ([]QuestionOption, error) {
	var opts []QuestionOption
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&opts); err != nil {
		return nil, err
	}
	return opts, nil
}
