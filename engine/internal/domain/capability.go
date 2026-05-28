package domain

import (
	"fmt"
	"time"
)

// CapabilityType represents the type of capability an agent can have.
type CapabilityType string

const (
	CapabilityTypeMemory          CapabilityType = "memory"
	CapabilityTypeKnowledge       CapabilityType = "knowledge"
	CapabilityTypeKnowledgeGraphs CapabilityType = "knowledge_graphs"
)

// AllCapabilityTypes returns all valid capability types.
func AllCapabilityTypes() []CapabilityType {
	return []CapabilityType{
		CapabilityTypeMemory,
		CapabilityTypeKnowledge,
		CapabilityTypeKnowledgeGraphs,
	}
}

// Capability represents a capability attached to an agent.
type Capability struct {
	ID        string
	AgentName string
	Type      CapabilityType
	Config    map[string]interface{}
	Enabled   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewCapability creates a new Capability with validation.
func NewCapability(agentName string, capType CapabilityType, config map[string]interface{}) (*Capability, error) {
	c := &Capability{
		AgentName: agentName,
		Type:      capType,
		Config:    config,
		Enabled:   true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if c.Config == nil {
		c.Config = make(map[string]interface{})
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// Validate validates the Capability.
func (c *Capability) Validate() error {
	if c.AgentName == "" {
		return fmt.Errorf("capability agent_name is required")
	}
	if !c.Type.IsValid() {
		return fmt.Errorf("invalid capability type: %s", c.Type)
	}
	return nil
}

// IsValid returns true if the capability type is one of the known types.
func (ct CapabilityType) IsValid() bool {
	switch ct {
	case CapabilityTypeMemory, CapabilityTypeKnowledge, CapabilityTypeKnowledgeGraphs:
		return true
	}
	return false
}
