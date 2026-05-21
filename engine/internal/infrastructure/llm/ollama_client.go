package llm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/ollama/ollama/api"

	"github.com/syntheticinc/syntheticbrew/pkg/config"
	"github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// OllamaClient implements LLM client for Ollama
type OllamaClient struct {
	client  *api.Client
	config  config.OllamaConfig
	baseURL string
}

// NewOllamaClient creates a new Ollama client
func NewOllamaClient(cfg config.OllamaConfig) (*OllamaClient, error) {
	// Parse base URL
	parsedURL, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, errors.Wrap(err, errors.CodeInvalidInput, "invalid ollama base URL")
	}

	// Create client with custom base URL
	client := api.NewClient(parsedURL, http.DefaultClient)

	return &OllamaClient{
		client:  client,
		config:  cfg,
		baseURL: cfg.BaseURL,
	}, nil
}

// Generate generates text using Ollama
func (c *OllamaClient) Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error) {
	model := req.Model
	if model == "" {
		model = c.config.Model
	}

	generateReq := &api.GenerateRequest{
		Model:  model,
		Prompt: req.Prompt,
		Stream: &req.Stream,
		Options: map[string]interface{}{
			"temperature": req.Temperature,
		},
	}

	if req.MaxTokens > 0 {
		generateReq.Options["num_predict"] = req.MaxTokens
	}

	var fullResponse string
	err := c.client.Generate(ctx, generateReq, func(resp api.GenerateResponse) error {
		fullResponse += resp.Response
		return nil
	})

	if err != nil {
		return nil, errors.Wrap(err, errors.CodeUnavailable, "ollama generation failed")
	}

	return &GenerateResponse{
		Content: fullResponse,
		Done:    true,
	}, nil
}

// GenerateStream generates text with streaming
func (c *OllamaClient) GenerateStream(ctx context.Context, req GenerateRequest, streamFunc func(chunk string) error) error {
	model := req.Model
	if model == "" {
		model = c.config.Model
	}

	stream := true
	generateReq := &api.GenerateRequest{
		Model:  model,
		Prompt: req.Prompt,
		Stream: &stream,
		Options: map[string]interface{}{
			"temperature": req.Temperature,
		},
	}

	if req.MaxTokens > 0 {
		generateReq.Options["num_predict"] = req.MaxTokens
	}

	err := c.client.Generate(ctx, generateReq, func(resp api.GenerateResponse) error {
		if resp.Response != "" {
			if err := streamFunc(resp.Response); err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		return errors.Wrap(err, errors.CodeUnavailable, "ollama streaming failed")
	}

	return nil
}

// CreateEmbedding creates embeddings using Ollama
func (c *OllamaClient) CreateEmbedding(ctx context.Context, req EmbeddingRequest) (*EmbeddingResponse, error) {
	model := req.Model
	if model == "" {
		model = c.config.Model
	}

	embedReq := &api.EmbedRequest{
		Model: model,
		Input: req.Input,
	}

	resp, err := c.client.Embed(ctx, embedReq)
	if err != nil {
		return nil, errors.Wrap(err, errors.CodeUnavailable, "ollama embedding failed")
	}

	// Convert []float64 to []float32
	embedding := make([]float32, len(resp.Embeddings[0]))
	for i, v := range resp.Embeddings[0] {
		embedding[i] = float32(v)
	}

	return &EmbeddingResponse{
		Embedding: embedding,
	}, nil
}

// Ping checks if Ollama is available
func (c *OllamaClient) Ping(ctx context.Context) error {
	// Try to list models as a health check
	_, err := c.client.List(ctx)
	if err != nil {
		return errors.Wrap(err, errors.CodeUnavailable, "ollama is not available")
	}
	return nil
}

// Close closes the Ollama client
func (c *OllamaClient) Close() error {
	// Ollama client doesn't require explicit cleanup
	return nil
}

// Chat performs a chat completion
func (c *OllamaClient) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	model := req.Model
	if model == "" {
		model = c.config.Model
	}

	messages := make([]api.Message, len(req.Messages))
	for i, msg := range req.Messages {
		messages[i] = api.Message{
			Role:    msg.Role,
			Content: msg.Content,
		}
	}

	chatReq := &api.ChatRequest{
		Model:    model,
		Messages: messages,
		Stream:   &req.Stream,
		Options: map[string]interface{}{
			"temperature": req.Temperature,
		},
	}

	if req.MaxTokens > 0 {
		chatReq.Options["num_predict"] = req.MaxTokens
	}

	var finalMessage api.Message
	err := c.client.Chat(ctx, chatReq, func(resp api.ChatResponse) error {
		finalMessage = resp.Message
		return nil
	})

	if err != nil {
		return nil, errors.Wrap(err, errors.CodeUnavailable, "ollama chat failed")
	}

	return &ChatResponse{
		Message: ChatMessage{
			Role:    finalMessage.Role,
			Content: finalMessage.Content,
		},
		Done: true,
	}, nil
}

// ChatStream performs a chat completion with streaming
func (c *OllamaClient) ChatStream(ctx context.Context, req ChatRequest, streamFunc func(chunk ChatMessage) error) error {
	model := req.Model
	if model == "" {
		model = c.config.Model
	}

	messages := make([]api.Message, len(req.Messages))
	for i, msg := range req.Messages {
		messages[i] = api.Message{
			Role:    msg.Role,
			Content: msg.Content,
		}
	}

	stream := true
	chatReq := &api.ChatRequest{
		Model:    model,
		Messages: messages,
		Stream:   &stream,
		Options: map[string]interface{}{
			"temperature": req.Temperature,
		},
	}

	if req.MaxTokens > 0 {
		chatReq.Options["num_predict"] = req.MaxTokens
	}

	err := c.client.Chat(ctx, chatReq, func(resp api.ChatResponse) error {
		if resp.Message.Content != "" {
			if err := streamFunc(ChatMessage{
				Role:    resp.Message.Role,
				Content: resp.Message.Content,
			}); err != nil {
				if err == io.EOF {
					return nil
				}
				return err
			}
		}
		return nil
	})

	if err != nil {
		return errors.Wrap(err, errors.CodeUnavailable, "ollama chat streaming failed")
	}

	return nil
}

// ListModels lists available models
func (c *OllamaClient) ListModels(ctx context.Context) ([]string, error) {
	resp, err := c.client.List(ctx)
	if err != nil {
		return nil, errors.Wrap(err, errors.CodeUnavailable, "failed to list ollama models")
	}

	models := make([]string, len(resp.Models))
	for i, model := range resp.Models {
		models[i] = model.Name
	}

	return models, nil
}

// GetModelInfo gets information about a specific model
func (c *OllamaClient) GetModelInfo(ctx context.Context, model string) (map[string]interface{}, error) {
	if model == "" {
		model = c.config.Model
	}

	resp, err := c.client.Show(ctx, &api.ShowRequest{
		Model: model,
	})
	if err != nil {
		return nil, errors.Wrap(err, errors.CodeNotFound, fmt.Sprintf("model %s not found", model))
	}

	info := map[string]interface{}{
		"model":      resp.ModelInfo["general.architecture"],
		"parameters": resp.ModelInfo["general.parameter_count"],
		"format":     resp.ModelInfo["general.file_type"],
	}

	return info, nil
}
