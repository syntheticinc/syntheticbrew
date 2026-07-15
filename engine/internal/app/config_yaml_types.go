package app

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// configYAML is the top-level structure for YAML import/export.
type configYAML struct {
	Agents          flexList[agentYAML]     `yaml:"agents,omitempty"`
	Models          flexList[modelYAML]     `yaml:"models,omitempty"`
	MCPServers      flexList[mcpServerYAML] `yaml:"mcp_servers,omitempty"`
	Schemas         []schemaYAML            `yaml:"schemas,omitempty"`
	KnowledgeGraphs []knowledgeGraphYAML    `yaml:"knowledge_graphs,omitempty"`
}

// schemaYAML round-trips a multi-agent schema: its identity, entry point and
// the delegation graph (agent_relations). Relations are the single source of
// truth for who may delegate to whom — the per-agent `can_spawn` field is
// derived, never imported.
type schemaYAML struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description,omitempty"`
	ChatEnabled bool           `yaml:"chat_enabled,omitempty"`
	EntryAgent  string         `yaml:"entry_agent,omitempty"`
	Relations   []relationYAML `yaml:"relations,omitempty"`
}

// relationYAML is one delegation arrow inside a schema: From may spawn To.
type relationYAML struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

// knowledgeGraphYAML is the bundle shape inside /config/import + /config/export
// for Knowledge Graphs (engine 1.3.0+). One bundle per array entry; round-trip
// `brewctl kg pull` produces byte-identical input for the same engine state.
type knowledgeGraphYAML struct {
	BundleName string                       `yaml:"bundle_name"`
	Version    string                       `yaml:"version,omitempty"`
	Schemas    []knowledgeGraphSchemaYAML   `yaml:"schemas"`
	Entities   []knowledgeGraphEntitiesYAML `yaml:"entities"`
}

type knowledgeGraphSchemaYAML struct {
	EntityType      string                 `yaml:"entity_type"`
	Schema          map[string]interface{} `yaml:"schema"`
	ExposeTools     []string               `yaml:"expose_tools,omitempty"`
	ToolDescription string                 `yaml:"tool_description,omitempty"`
}

type knowledgeGraphEntitiesYAML struct {
	EntityType string                   `yaml:"entity_type"`
	Items      []map[string]interface{} `yaml:"items"`
}

// namedItem is implemented by YAML structs that can be keyed by name in map format.
type namedItem interface {
	agentYAML | modelYAML | mcpServerYAML
}

// flexList accepts both YAML array format and map format (where map keys become the Name/Title field).
// Map format example:
//
//	agents:
//	  my-agent:
//	    model_name: glm-5
//
// Array format example:
//
//	agents:
//	  - name: my-agent
//	    model_name: glm-5
type flexList[T namedItem] struct {
	Items []T
}

// MarshalYAML marshals as a plain array so export always uses the array format.
func (f flexList[T]) MarshalYAML() (interface{}, error) {
	return f.Items, nil
}

// UnmarshalYAML tries array format first, then map format.
func (f *flexList[T]) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.SequenceNode {
		return node.Decode(&f.Items)
	}
	if node.Kind == yaml.MappingNode {
		return f.decodeMap(node)
	}
	if node.Kind == yaml.ScalarNode && (node.Tag == "!!null" || node.Value == "") {
		return nil
	}
	return fmt.Errorf("expected sequence or mapping, got %v", node.Kind)
}

func (f *flexList[T]) decodeMap(node *yaml.Node) error {
	// Mapping nodes have key-value pairs: [key1, val1, key2, val2, ...]
	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valNode := node.Content[i+1]

		var item T
		if err := valNode.Decode(&item); err != nil {
			return fmt.Errorf("decode item %q: %w", keyNode.Value, err)
		}
		setNameFromKey(&item, keyNode.Value)
		f.Items = append(f.Items, item)
	}
	return nil
}

// setNameFromKey injects the map key into the appropriate name field of the item.
func setNameFromKey(item interface{}, key string) {
	switch v := item.(type) {
	case *agentYAML:
		if v.Name == "" {
			v.Name = key
		}
	case *modelYAML:
		if v.Name == "" {
			v.Name = key
		}
	case *mcpServerYAML:
		if v.Name == "" {
			v.Name = key
		}
	}
}

type agentYAML struct {
	Name            string   `yaml:"name"`
	SystemPrompt    string   `yaml:"system_prompt"`
	ModelName       string   `yaml:"model_name,omitempty"`
	Lifecycle       string   `yaml:"lifecycle"`
	ToolExecution   string   `yaml:"tool_execution"`
	MaxSteps        int      `yaml:"max_steps"`
	MaxContextSize  int      `yaml:"max_context_size"`
	MaxTurnDuration int      `yaml:"max_turn_duration"`
	MaxStepDuration int      `yaml:"max_step_duration"`
	Temperature     *float64 `yaml:"temperature,omitempty"`
	TopP            *float64 `yaml:"top_p,omitempty"`
	MaxTokens       *int     `yaml:"max_tokens,omitempty"`
	StopSequences   []string `yaml:"stop_sequences,omitempty"`
	ConfirmBefore   []string `yaml:"confirm_before,omitempty"`
	Tools           []string `yaml:"tools,omitempty"`
	CanSpawn        []string `yaml:"can_spawn,omitempty"`
	MCPServers      []string `yaml:"mcp_servers,omitempty"`
}

// UnmarshalYAML supports field aliases used in documentation:
//   - "system" as alias for "system_prompt"
//   - "model" as alias for "model_name"
func (a *agentYAML) UnmarshalYAML(node *yaml.Node) error {
	type agentYAMLAlias agentYAML
	var alias agentYAMLAlias
	if err := node.Decode(&alias); err != nil {
		return err
	}
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i].Value
			val := node.Content[i+1]
			switch key {
			case "system":
				if alias.SystemPrompt == "" {
					alias.SystemPrompt = val.Value
				}
			case "model":
				if alias.ModelName == "" {
					alias.ModelName = val.Value
				}
			}
		}
	}
	*a = agentYAML(alias)
	return nil
}

type modelYAML struct {
	Name      string         `yaml:"name"`
	Type      string         `yaml:"type"`
	BaseURL   string         `yaml:"base_url,omitempty"`
	ModelName string         `yaml:"model_name"`
	APIKey    string         `yaml:"api_key,omitempty"`
	ExtraBody map[string]any `yaml:"extra_body,omitempty"`
}

// resolvedType returns the canonical model type.
func (m modelYAML) resolvedType() string {
	return m.Type
}

type mcpServerYAML struct {
	Name           string            `yaml:"name"`
	Type           string            `yaml:"type"`
	Command        string            `yaml:"command,omitempty"`
	Args           []string          `yaml:"args,omitempty"`
	URL            string            `yaml:"url,omitempty"`
	EnvVars        map[string]string `yaml:"env_vars,omitempty"`
	ForwardHeaders []string          `yaml:"forward_headers,omitempty"`
}
