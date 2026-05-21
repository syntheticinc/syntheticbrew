package llm

import (
	"context"

	"github.com/syntheticinc/syntheticbrew/pkg/errors"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// EinoChatModelAdapter adapts our LLM clients to Eino ChatModel interface
type EinoChatModelAdapter struct {
	client Client
	tools  []*schema.ToolInfo
}

// NewEinoChatModelAdapter creates a new Eino ChatModel adapter
func NewEinoChatModelAdapter(client Client) model.ChatModel {
	return &EinoChatModelAdapter{
		client: client,
		tools:  nil,
	}
}

// Generate implements model.BaseChatModel interface
func (a *EinoChatModelAdapter) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	if len(input) == 0 {
		return nil, errors.New(errors.CodeInvalidInput, "input messages cannot be empty")
	}

	// Convert Eino messages to our ChatMessage format
	messages := make([]ChatMessage, 0, len(input))
	for _, msg := range input {
		messages = append(messages, ChatMessage{
			Role:    string(msg.Role),
			Content: msg.Content,
		})
	}

	// Create chat request
	req := ChatRequest{
		Messages: messages,
	}

	// Call our client
	resp, err := a.client.Chat(ctx, req)
	if err != nil {
		return nil, errors.Wrap(err, errors.CodeInternal, "chat request failed")
	}

	// Convert response to Eino message
	return &schema.Message{
		Role:    schema.Assistant,
		Content: resp.Message.Content,
	}, nil
}

// Stream implements model.BaseChatModel interface for streaming
func (a *EinoChatModelAdapter) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	if len(input) == 0 {
		return nil, errors.New(errors.CodeInvalidInput, "input messages cannot be empty")
	}

	// Convert Eino messages to our ChatMessage format
	messages := make([]ChatMessage, 0, len(input))
	for _, msg := range input {
		messages = append(messages, ChatMessage{
			Role:    string(msg.Role),
			Content: msg.Content,
		})
	}

	// Create chat request
	req := ChatRequest{
		Messages: messages,
	}

	// Create Eino pipe for streaming
	sr, sw := schema.Pipe[*schema.Message](10)

	// Start streaming in goroutine
	go func() {
		defer sw.Close()

		err := a.client.ChatStream(ctx, req, func(chunk ChatMessage) error {
			// Send chunk as Eino message through pipe
			msg := &schema.Message{
				Role:    schema.Assistant,
				Content: chunk.Content,
			}

			// Send returns bool indicating if channel is closed
			closed := sw.Send(msg, nil)
			if closed {
				return errors.New(errors.CodeInternal, "stream channel closed")
			}

			return nil
		})

		if err != nil {
			// Send error through pipe
			sw.Send(nil, errors.Wrap(err, errors.CodeInternal, "chat stream failed"))
		}
	}()

	return sr, nil
}

// BindTools implements model.ChatModel interface for tool binding
func (a *EinoChatModelAdapter) BindTools(tools []*schema.ToolInfo) error {
	// Store tools for future use
	a.tools = tools
	return nil
}

// GetType returns the model type
func (a *EinoChatModelAdapter) GetType() string {
	return "chat_model"
}

// IsCallbacksEnabled returns whether callbacks are enabled
func (a *EinoChatModelAdapter) IsCallbacksEnabled() bool {
	return false
}
