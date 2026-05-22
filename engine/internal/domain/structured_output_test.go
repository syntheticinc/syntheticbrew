package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestQuestionUnmarshal_LiteralOptions(t *testing.T) {
	const data = `{
		"id": "platform",
		"label": "Platform?",
		"type": "select",
		"options": [
			{"label": "iOS"},
			{"label": "Android", "value": "android"}
		]
	}`

	var q Question
	if err := json.Unmarshal([]byte(data), &q); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q.ID != "platform" || q.Label != "Platform?" || q.Type != "select" {
		t.Fatalf("unexpected fields: %+v", q)
	}
	if len(q.Options) != 2 {
		t.Fatalf("expected 2 options, got %d", len(q.Options))
	}
	if q.Options[0].Label != "iOS" || q.Options[1].Value != "android" {
		t.Fatalf("unexpected options: %+v", q.Options)
	}
}

func TestQuestionUnmarshal_StringifiedOptions(t *testing.T) {
	const data = `{
		"id": "platform",
		"label": "Platform?",
		"type": "select",
		"options": "[{\"label\":\"iOS\"},{\"label\":\"Android\",\"value\":\"android\"}]"
	}`

	var q Question
	if err := json.Unmarshal([]byte(data), &q); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(q.Options) != 2 {
		t.Fatalf("expected 2 options, got %d", len(q.Options))
	}
	if q.Options[0].Label != "iOS" || q.Options[1].Value != "android" {
		t.Fatalf("unexpected options: %+v", q.Options)
	}
}

func TestQuestionUnmarshal_EmptyStringifiedOptions(t *testing.T) {
	const data = `{
		"id": "name",
		"label": "Name",
		"type": "text",
		"options": ""
	}`

	var q Question
	if err := json.Unmarshal([]byte(data), &q); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q.Options != nil {
		t.Fatalf("expected nil options for empty string, got %+v", q.Options)
	}
}

func TestQuestionUnmarshal_MissingOptions(t *testing.T) {
	const data = `{"id": "name", "label": "Name", "type": "text"}`

	var q Question
	if err := json.Unmarshal([]byte(data), &q); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q.Options != nil {
		t.Fatalf("expected nil options when field omitted, got %+v", q.Options)
	}
}

func TestQuestionUnmarshal_MalformedStringifiedOptions(t *testing.T) {
	const data = `{
		"id": "platform",
		"label": "Platform?",
		"type": "select",
		"options": "["
	}`

	var q Question
	err := json.Unmarshal([]byte(data), &q)
	if err == nil {
		t.Fatal("expected error on malformed stringified options, got nil")
	}
	if !strings.Contains(err.Error(), "options") {
		t.Fatalf("expected error to mention options, got %q", err.Error())
	}
}

func TestQuestionUnmarshal_RejectsUnknownTopLevelField(t *testing.T) {
	const data = `{
		"id": "platform",
		"label": "Platform?",
		"type": "select",
		"options": [{"label":"iOS"}],
		"extra": "unexpected"
	}`

	var q Question
	err := json.Unmarshal([]byte(data), &q)
	if err == nil {
		t.Fatal("expected error on unknown field, got nil")
	}
	if !strings.Contains(err.Error(), "extra") {
		t.Fatalf("expected error to mention the unknown field name, got %q", err.Error())
	}
}

func TestQuestionUnmarshal_RejectsUnknownOptionField(t *testing.T) {
	const data = `{
		"id": "platform",
		"label": "Platform?",
		"type": "select",
		"options": [{"label":"iOS","invented":"oops"}]
	}`

	var q Question
	err := json.Unmarshal([]byte(data), &q)
	if err == nil {
		t.Fatal("expected error on unknown field inside options, got nil")
	}
	if !strings.Contains(err.Error(), "invented") {
		t.Fatalf("expected error to mention the unknown field, got %q", err.Error())
	}
}

func TestQuestionUnmarshal_RejectsUnknownFieldInStringifiedOptions(t *testing.T) {
	const data = `{
		"id": "platform",
		"label": "Platform?",
		"type": "select",
		"options": "[{\"label\":\"iOS\",\"invented\":\"oops\"}]"
	}`

	var q Question
	err := json.Unmarshal([]byte(data), &q)
	if err == nil {
		t.Fatal("expected error on unknown field inside stringified options, got nil")
	}
	if !strings.Contains(err.Error(), "invented") {
		t.Fatalf("expected error to mention the unknown field, got %q", err.Error())
	}
}
