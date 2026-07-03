package llm

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/model"
)

// ModelSelector selects a ChatModel based on agent name.
// Allows different agent roles to use different LLM models.
// Also supports named model resolution for per-agent model configuration.
type ModelSelector struct {
	models       map[string]model.ToolCallingChatModel
	defaultModel model.ToolCallingChatModel
	modelNames   map[string]string
	defaultName  string
	namedModels  map[string]model.ToolCallingChatModel
}

// NewModelSelector creates a new ModelSelector with a default model.
func NewModelSelector(defaultModel model.ToolCallingChatModel, defaultName string) *ModelSelector {
	return &ModelSelector{
		models:       make(map[string]model.ToolCallingChatModel),
		defaultModel: defaultModel,
		modelNames:   make(map[string]string),
		defaultName:  defaultName,
		namedModels:  make(map[string]model.ToolCallingChatModel),
	}
}

// SetModel sets a specific model for a given agent name.
func (s *ModelSelector) SetModel(agentName string, m model.ToolCallingChatModel, name string) {
	s.models[agentName] = m
	s.modelNames[agentName] = name
}

// SetDefault replaces the fallback model returned by Select (and its name from
// ModelName) when an agent has no per-agent model. It is the extension point a
// plugin uses to install a process-wide default — e.g. a shared proxy client —
// without the engine knowing what that default is.
func (s *ModelSelector) SetDefault(m model.ToolCallingChatModel, name string) {
	s.defaultModel = m
	s.defaultName = name
}

// Select returns the ChatModel for the given agent name.
// Falls back to default if no specific model is configured.
func (s *ModelSelector) Select(agentName string) model.ToolCallingChatModel {
	if m, ok := s.models[agentName]; ok {
		return m
	}
	return s.defaultModel
}

// ModelName returns the model name for the given agent name.
// Falls back to default name if no specific name is configured.
func (s *ModelSelector) ModelName(agentName string) string {
	if name, ok := s.modelNames[agentName]; ok {
		return name
	}
	return s.defaultName
}

// RegisterNamedModel registers a model under a given name for per-agent resolution.
// Agents configured with a model name (e.g., "llama-4") can resolve it via ResolveByName.
func (s *ModelSelector) RegisterNamedModel(name string, m model.ToolCallingChatModel) {
	s.namedModels[name] = m
}

// ResolveByName returns a model registered under the given name.
// Returns an error if the name is not found.
// ctx is accepted for interface uniformity (ctx-doctrine); ModelSelector holds a
// process-global in-memory map built at startup — there is no per-tenant dispatch here.
func (s *ModelSelector) ResolveByName(_ context.Context, name string) (model.ToolCallingChatModel, error) {
	m, ok := s.namedModels[name]
	if !ok {
		return nil, fmt.Errorf("named model %q not registered", name)
	}
	return m, nil
}

// NamedModelCount returns the number of registered named models.
func (s *ModelSelector) NamedModelCount() int {
	return len(s.namedModels)
}
