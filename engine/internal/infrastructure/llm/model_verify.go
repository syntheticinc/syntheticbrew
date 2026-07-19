package llm

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// VerifyResult contains the result of model connectivity verification.
type VerifyResult struct {
	Connectivity   string
	ToolCalling    string
	ResponseTimeMs int64
	ModelName      string
	Provider       string
	Error          *string
}

// knownToolCallingProviders lists providers whose models are known to support tool calling.
// For these providers we skip the tool probe and return "skipped".
var knownToolCallingProviders = map[string]bool{
	"openai":       true,
	"anthropic":    true,
	"google":       true,
	"mistral":      true,
	"azure_openai": true,
}

// VerifyModel checks that a model is accessible and optionally probes tool calling support.
// It creates a temporary client, sends a ping message, and if the provider is not known
// to support tools, sends a tool probe request.
// The context should have a reasonable timeout (e.g. 30 seconds).
func VerifyModel(ctx context.Context, client model.ToolCallingChatModel, modelName, provider string) *VerifyResult {
	result := &VerifyResult{
		ModelName: modelName,
		Provider:  provider,
	}

	// Step 1: Ping — simple chat completion.
	start := time.Now()
	pingMessages := []*schema.Message{
		{Role: schema.User, Content: "Say hi"},
	}
	_, err := client.Generate(ctx, pingMessages)
	result.ResponseTimeMs = time.Since(start).Milliseconds()

	if err != nil {
		// N1: an egress-policy rejection returns a single opaque message so the
		// verify endpoint cannot be used as a private-host/port scan oracle. Real
		// connection errors to permitted hosts keep their detail for debugging.
		errMsg := fmt.Sprintf("connectivity check failed: %s", err.Error())
		if opaque, blocked := normalizeEgressError(err); blocked {
			errMsg = opaque
		}
		result.Connectivity = "error"
		result.ToolCalling = "skipped"
		result.Error = &errMsg
		return result
	}
	result.Connectivity = "ok"

	// Known provider optimization — skip tool probe.
	if knownToolCallingProviders[provider] {
		result.ToolCalling = "skipped"
		return result
	}

	// Step 2: Tool probe — chat with a test tool.
	result.ToolCalling = probeToolCalling(ctx, client)
	return result
}

func probeToolCalling(ctx context.Context, client model.ToolCallingChatModel) string {
	toolInfo := &schema.ToolInfo{
		Name: "calculator",
		Desc: "Performs basic math calculations",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"expression": {
				Type:     "string",
				Desc:     "The math expression to evaluate",
				Required: true,
			},
		}),
	}

	clientWithTools, err := client.WithTools([]*schema.ToolInfo{toolInfo})
	if err != nil {
		return "error"
	}

	toolMessages := []*schema.Message{
		{Role: schema.User, Content: "What is 2+2? Use the calculator tool."},
	}
	toolResp, err := clientWithTools.Generate(ctx, toolMessages)
	if err != nil {
		return "error"
	}

	if len(toolResp.ToolCalls) > 0 {
		return "supported"
	}
	return "not_detected"
}
