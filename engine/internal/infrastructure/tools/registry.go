package tools

import (
	"sync"

	"github.com/syntheticinc/syntheticbrew/pkg/errors"
	"github.com/cloudwego/eino/components/tool"
)

// Registry manages available tools for the agent
type Registry struct {
	mu    sync.RWMutex
	tools map[string]tool.InvokableTool
}

// NewRegistry creates a new tool registry
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]tool.InvokableTool),
	}
}

// Register adds a tool to the registry
func (r *Registry) Register(name string, t tool.InvokableTool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tools[name]; exists {
		return errors.New(errors.CodeAlreadyExists, "tool already registered: "+name)
	}

	r.tools[name] = t
	return nil
}

// Unregister removes a tool from the registry
func (r *Registry) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tools[name]; !exists {
		return errors.New(errors.CodeNotFound, "tool not found: "+name)
	}

	delete(r.tools, name)
	return nil
}

// Get retrieves a tool by name
func (r *Registry) Get(name string) (tool.InvokableTool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	t, exists := r.tools[name]
	if !exists {
		return nil, errors.New(errors.CodeNotFound, "tool not found: "+name)
	}

	return t, nil
}

// GetAll returns all registered tools as BaseTool slice
func (r *Registry) GetAll() []tool.BaseTool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tools := make([]tool.BaseTool, 0, len(r.tools))
	for _, t := range r.tools {
		tools = append(tools, t)
	}

	return tools
}

// Count returns the number of registered tools
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.tools)
}
