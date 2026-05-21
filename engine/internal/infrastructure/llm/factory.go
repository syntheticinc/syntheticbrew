package llm

import (
	"fmt"
	"sync"

	"github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// ClientFactory creates LLM clients based on provider type
type ClientFactory struct {
	mu        sync.RWMutex
	providers map[string]ProviderConstructor
}

// ProviderConstructor is a function that creates a new LLM client
type ProviderConstructor func(config ProviderConfig) (Client, error)

// ProviderConfig holds configuration for LLM provider
type ProviderConfig struct {
	BaseURL     string
	APIKey      string
	Model       string
	Temperature float32
	MaxTokens   int
}

// NewClientFactory creates a new LLM client factory
func NewClientFactory() *ClientFactory {
	return &ClientFactory{
		providers: make(map[string]ProviderConstructor),
	}
}

// Register registers a new provider constructor
func (f *ClientFactory) Register(providerType string, constructor ProviderConstructor) error {
	if providerType == "" {
		return errors.New(errors.CodeInvalidInput, "provider type is required")
	}
	if constructor == nil {
		return errors.New(errors.CodeInvalidInput, "constructor is required")
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if _, exists := f.providers[providerType]; exists {
		return errors.New(errors.CodeAlreadyExists, fmt.Sprintf("provider %s already registered", providerType))
	}

	f.providers[providerType] = constructor
	return nil
}

// Unregister removes a provider constructor
func (f *ClientFactory) Unregister(providerType string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, exists := f.providers[providerType]; !exists {
		return errors.New(errors.CodeNotFound, fmt.Sprintf("provider %s not found", providerType))
	}

	delete(f.providers, providerType)
	return nil
}

// Create creates a new LLM client for the specified provider
func (f *ClientFactory) Create(providerType string, config ProviderConfig) (Client, error) {
	f.mu.RLock()
	constructor, exists := f.providers[providerType]
	f.mu.RUnlock()

	if !exists {
		return nil, errors.New(errors.CodeNotFound, fmt.Sprintf("provider %s not found", providerType))
	}

	client, err := constructor(config)
	if err != nil {
		return nil, errors.Wrap(err, errors.CodeInternal, fmt.Sprintf("failed to create %s client", providerType))
	}

	return client, nil
}

// ListProviders returns all registered provider types
func (f *ClientFactory) ListProviders() []string {
	f.mu.RLock()
	defer f.mu.RUnlock()

	providers := make([]string, 0, len(f.providers))
	for providerType := range f.providers {
		providers = append(providers, providerType)
	}
	return providers
}

// HasProvider checks if a provider is registered
func (f *ClientFactory) HasProvider(providerType string) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()

	_, exists := f.providers[providerType]
	return exists
}

// Global factory instance
var globalFactory = NewClientFactory()

// RegisterProvider registers a provider in the global factory
func RegisterProvider(providerType string, constructor ProviderConstructor) error {
	return globalFactory.Register(providerType, constructor)
}

// CreateClient creates a client using the global factory
func CreateClient(providerType string, config ProviderConfig) (Client, error) {
	return globalFactory.Create(providerType, config)
}

// GetFactory returns the global factory instance
func GetFactory() *ClientFactory {
	return globalFactory
}
