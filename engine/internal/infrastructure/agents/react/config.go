package react

import (
	"github.com/syntheticinc/syntheticbrew/pkg/config"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// ToolCallRecorder defines interface for recording tool calls and results.
// Consumer-side interface: defined here where it's used.
type ToolCallRecorder interface {
	RecordToolCall(sessionID, toolName string)
	RecordToolResult(sessionID, toolName, result string)
}

// AgentConfig holds configuration for ReAct agent
type AgentConfig struct {
	ChatModel                model.ToolCallingChatModel
	Tools                    []tool.BaseTool
	MaxSteps                 int
	SessionID                string
	AgentConfig              *config.AgentConfig
	ModelName                string            // Model name for reasoning extraction
	HistoryMessages          []*schema.Message // Conversation history (user/assistant messages)
	ContextReminderProviders []ContextReminderProvider // External context reminder providers (e.g., WorkContextReminder)
	ToolCallRecorder         ToolCallRecorder  // Records tool calls for efficiency reminders
	SequentialTools          bool   // if true, tool calls execute sequentially (not parallel)
	AgentID                  string // "supervisor" | "code-agent-xxx" (for log separation)
	ParentAgentID            string // parent agent ID (for Code Agents → "supervisor")
	SubtaskID                string // subtask being executed (for Code Agents)
	SessionDirName           string // shared session dir name (set by parent to keep all logs together)
	ProviderType    string // e.g. "openai", "openai_compatible", "anthropic"
	ProviderBaseURL string // upstream API base URL — paired with ProviderType/ModelName for route detection
}
