package domain

import (
	"testing"
)

func TestNewCapability_Valid(t *testing.T) {
	c, err := NewCapability("my-agent", CapabilityTypeMemory, map[string]interface{}{"retention_days": 30})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.AgentName != "my-agent" {
		t.Errorf("expected agent %q, got %q", "my-agent", c.AgentName)
	}
	if c.Type != CapabilityTypeMemory {
		t.Errorf("expected type %q, got %q", CapabilityTypeMemory, c.Type)
	}
	if !c.Enabled {
		t.Error("expected enabled to be true by default")
	}
}

func TestNewCapability_NilConfig(t *testing.T) {
	c, err := NewCapability("agent", CapabilityTypeKnowledge, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Config == nil {
		t.Error("expected config to be initialized to empty map")
	}
}

func TestNewCapability_EmptyAgent(t *testing.T) {
	_, err := NewCapability("", CapabilityTypeMemory, nil)
	if err == nil {
		t.Fatal("expected error for empty agent name")
	}
}

func TestNewCapability_InvalidType(t *testing.T) {
	_, err := NewCapability("agent", CapabilityType("invalid"), nil)
	if err == nil {
		t.Fatal("expected error for invalid capability type")
	}
}

func TestCapabilityType_IsValid(t *testing.T) {
	tests := []struct {
		capType CapabilityType
		valid   bool
	}{
		{CapabilityTypeMemory, true},
		{CapabilityTypeKnowledge, true},
		{CapabilityType("guardrail"), false},
		{CapabilityType("recovery"), false},
		{CapabilityType("policies"), false},
		{CapabilityType("invalid"), false},
		{CapabilityType(""), false},
	}
	for _, tt := range tests {
		t.Run(string(tt.capType), func(t *testing.T) {
			if got := tt.capType.IsValid(); got != tt.valid {
				t.Errorf("IsValid() = %v, want %v", got, tt.valid)
			}
		})
	}
}

func TestAllCapabilityTypes(t *testing.T) {
	types := AllCapabilityTypes()
	if len(types) != 3 {
		t.Errorf("expected 3 capability types, got %d", len(types))
	}
	for _, ct := range types {
		if !ct.IsValid() {
			t.Errorf("AllCapabilityTypes() returned invalid type: %s", ct)
		}
	}
	// Sanity: knowledge_graphs must be in the list (1.3.0 capability).
	seen := false
	for _, ct := range types {
		if ct == CapabilityTypeKnowledgeGraphs {
			seen = true
		}
	}
	if !seen {
		t.Error("AllCapabilityTypes() must include CapabilityTypeKnowledgeGraphs")
	}
}
