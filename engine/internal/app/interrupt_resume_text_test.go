package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// HITL Interrupt Primitive — Q+A reconstruction tests (engine 1.2.0).
//
// buildResumeLLMText reconstructs a natural-language user message representing
// the user's response to a HITL interrupt. Validated on real shapes that the
// React loop consumes after resume_interrupt POSTs.

func TestBuildResumeLLMText_StructuredOutput_QAPairs(t *testing.T) {
	schema := json.RawMessage(`{
		"output_type": "form",
		"title": "Configure deployment",
		"questions": [
			{"id": "criticality", "label": "Критичность", "type": "select",
				"options": [
					{"value": "high",   "label": "Холодовая цепь"},
					{"value": "normal", "label": "Обычная отправка"}
				]
			}
		]
	}`)
	payload := json.RawMessage(`{
		"answers": [
			{"question_id": "criticality", "value": "high", "label": "Холодовая цепь"}
		]
	}`)

	out := buildResumeLLMText(domain.InterruptKindStructuredOutput, schema, payload)

	assert.True(t, strings.HasPrefix(out, "User submitted the form:"))
	assert.Contains(t, out, "Q: Критичность? A: Холодовая цепь")
	assert.NotContains(t, out, "high", "LLM should see human label, not raw value")
}

func TestBuildResumeLLMText_StructuredOutput_FallsBackToValueWhenLabelMissing(t *testing.T) {
	schema := json.RawMessage(`{
		"output_type": "form",
		"questions": [{"id":"q1","label":"Question?","type":"text"}]
	}`)
	payload := json.RawMessage(`{
		"answers": [{"question_id": "q1", "value": "manual-text-answer"}]
	}`)

	out := buildResumeLLMText(domain.InterruptKindStructuredOutput, schema, payload)

	assert.Contains(t, out, "Q: Question? A: manual-text-answer")
}

func TestBuildResumeLLMText_StructuredOutput_FallsBackToQuestionIDWhenLabelMissing(t *testing.T) {
	// Schema with empty questions array — should still render Q+A using
	// question_id as question label fallback.
	schema := json.RawMessage(`{"output_type": "form", "questions": []}`)
	payload := json.RawMessage(`{
		"answers": [{"question_id": "criticality", "value": "high", "label": "High"}]
	}`)

	out := buildResumeLLMText(domain.InterruptKindStructuredOutput, schema, payload)

	assert.Contains(t, out, "Q: criticality? A: High")
}

func TestBuildResumeLLMText_StructuredOutput_MultipleQuestions(t *testing.T) {
	schema := json.RawMessage(`{
		"output_type": "form",
		"questions": [
			{"id": "platform", "label": "Platform?", "type": "select",
				"options": [{"value":"ios","label":"iOS"},{"value":"android","label":"Android"}]},
			{"id": "version", "label": "Version", "type": "text"}
		]
	}`)
	payload := json.RawMessage(`{
		"answers": [
			{"question_id": "platform", "value": "ios", "label": "iOS"},
			{"question_id": "version", "value": "1.2.0"}
		]
	}`)

	out := buildResumeLLMText(domain.InterruptKindStructuredOutput, schema, payload)

	assert.Contains(t, out, "Q: Platform? A: iOS")
	// "Version" lacks a trailing ?, so the helper adds one for natural prose.
	assert.Contains(t, out, "Q: Version? A: 1.2.0")
}

func TestBuildResumeLLMText_StructuredOutput_EmptyAnswers(t *testing.T) {
	schema := json.RawMessage(`{"output_type":"form","questions":[]}`)
	payload := json.RawMessage(`{"answers": []}`)

	out := buildResumeLLMText(domain.InterruptKindStructuredOutput, schema, payload)

	assert.Equal(t, "User submitted the form (no answers provided).", out)
}

func TestBuildResumeLLMText_UnknownKind_FallsBackToRawPayload(t *testing.T) {
	schema := json.RawMessage(`{}`)
	payload := json.RawMessage(`{"file_id":"abc","mime":"application/pdf"}`)

	out := buildResumeLLMText(domain.InterruptKind("file_pick"), schema, payload)

	assert.Contains(t, out, "User submitted form response:")
	assert.Contains(t, out, "file_id")
}

func TestBuildResumeLLMText_StructuredOutput_MalformedPayloadFallsBack(t *testing.T) {
	schema := json.RawMessage(`{"output_type":"form","questions":[]}`)
	payload := json.RawMessage(`not valid json`)

	out := buildResumeLLMText(domain.InterruptKindStructuredOutput, schema, payload)

	assert.Contains(t, out, "User submitted form response:")
}
