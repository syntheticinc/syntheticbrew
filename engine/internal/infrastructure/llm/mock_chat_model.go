package llm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// MockChatModel implements model.ToolCallingChatModel
// Returns predefined responses based on scenario and message history
type MockChatModel struct {
	scenario string
}

// NewMockChatModel creates a new MockChatModel
func NewMockChatModel(scenario string) *MockChatModel {
	return &MockChatModel{scenario: scenario}
}

// Generate implements model.ChatModel.Generate
func (m *MockChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	hasToolResult := containsToolResult(input)

	switch m.scenario {
	case "echo":
		return textMessage("Hello, world!"), nil

	case "server-tool":
		if hasToolResult {
			return textMessage("Task operation complete."), nil
		}
		return toolCallMessage("manage_tasks", `{"action":"list_subtasks","parent_task_id":"test-1"}`), nil

	case "reasoning":
		return reasoningMessage("Let me think...", "The answer is 42."), nil

	case "error":
		return nil, fmt.Errorf("mock LLM error: simulated failure")

	case "proxied-read":
		if hasToolResult {
			return textMessage(fmt.Sprintf("File contains: %s", extractLastToolResult(input))), nil
		}
		return toolCallMessage("read_file", `{"file_path":"src/main.ts"}`), nil

	case "proxied-write":
		if hasToolResult {
			return textMessage("File written successfully."), nil
		}
		return toolCallMessage("write_file", `{"file_path":"output.txt","content":"hello"}`), nil

	case "proxied-exec":
		if hasToolResult {
			return textMessage(fmt.Sprintf("Command output: %s", extractLastToolResult(input))), nil
		}
		return toolCallMessage("execute_command", `{"command":"echo test"}`), nil

	case "multi-tool":
		toolCount := countToolResults(input)
		switch {
		case toolCount == 0:
			return toolCallMessage("read_file", `{"file_path":"a.ts"}`), nil
		case toolCount == 1:
			msg := &schema.Message{
				Role: schema.Assistant,
				ToolCalls: []schema.ToolCall{{
					ID:   "call_mock_2",
					Type: "function",
					Function: schema.FunctionCall{
						Name:      "read_file",
						Arguments: `{"file_path":"b.ts"}`,
					},
				}},
				ResponseMeta: &schema.ResponseMeta{
					FinishReason: "tool_calls",
				},
			}
			return msg, nil
		default:
			return textMessage("All files read successfully."), nil
		}

	case "tool-error":
		if hasToolResult {
			return textMessage("Handled error gracefully."), nil
		}
		return toolCallMessage("read_file", `{"file_path":"nonexistent.ts"}`), nil

	case "task-create":
		if hasToolResult {
			return textMessage(fmt.Sprintf("Task result: %s", extractLastToolResult(input))), nil
		}
		return toolCallMessage("manage_tasks", `{"action":"create","title":"Test Task","description":"Implement feature X","acceptance_criteria":["Tests pass","Code reviewed"]}`), nil

	case "proxied-edit":
		if hasToolResult {
			return textMessage("Edit applied successfully."), nil
		}
		return toolCallMessage("edit_file", `{"file_path":"src/app.ts","old_string":"console.log('old')","new_string":"console.log('new')"}`), nil

	case "proxied-tree":
		if hasToolResult {
			return textMessage(fmt.Sprintf("Project structure: %s", extractLastToolResult(input))), nil
		}
		return toolCallMessage("get_project_tree", `{}`), nil

	case "proxied-search":
		if hasToolResult {
			return textMessage(fmt.Sprintf("Search results: %s", extractLastToolResult(input))), nil
		}
		return toolCallMessage("search_code", `{"query":"hello world"}`), nil

	case "multi-agent":
		if isCodeAgentCall(input) {
			return textMessage("Code agent: task completed successfully."), nil
		}
		if hasToolResult {
			return textMessage("All agents completed. Work is done."), nil
		}
		return toolCallMessage("spawn_agent", `{"action":"spawn","subtask_id":"test-subtask-1"}`), nil

	case "agent-interrupt":
		if isCodeAgentCall(input) {
			time.Sleep(5 * time.Second)
			return textMessage("Code agent: task completed."), nil
		}
		if hasToolResult {
			lastResult := extractLastToolResult(input)
			if strings.Contains(lastResult, "[INTERRUPT]") {
				return textMessage("Supervisor: received user interrupt, handling message."), nil
			}
			return textMessage("Supervisor: all agents completed successfully."), nil
		}
		return toolCallMessage("spawn_agent", `{"action":"spawn","subtask_id":"test-subtask-1"}`), nil

	case "smart-search":
		return toolCallAndReturn("smart_search", `{"query":"handleError error handling","limit":10}`, "SEARCH_RESULT:", hasToolResult, input)

	case "smart-search-exact":
		return toolCallAndReturn("smart_search", `{"query":"handleError","limit":10}`, "SEARCH_RESULT:", hasToolResult, input)

	case "smart-search-broad":
		return toolCallAndReturn("smart_search", `{"query":"error handling patterns","limit":10}`, "SEARCH_RESULT:", hasToolResult, input)

	case "smart-search-symbol":
		return toolCallAndReturn("smart_search", `{"query":"DomainError","limit":10}`, "SEARCH_RESULT:", hasToolResult, input)

	case "smart-search-cross-file":
		return toolCallAndReturn("smart_search", `{"query":"http handler request","limit":10}`, "SEARCH_RESULT:", hasToolResult, input)

	case "smart-search-no-match":
		return toolCallAndReturn("smart_search", `{"query":"kubernetes deployment yaml","limit":10}`, "SEARCH_RESULT:", hasToolResult, input)

	case "grep-direct":
		return toolCallAndReturn("grep_search", `{"pattern":"func\\s+handle","include":"*.go","limit":10}`, "GREP_RESULT:", hasToolResult, input)

	case "glob-search":
		return toolCallAndReturn("glob", `{"pattern":"**/*.go","limit":10}`, "GLOB_RESULT:", hasToolResult, input)

	case "compare-exact-grep":
		return toolCallAndReturn("grep_search", `{"pattern":"handleError","limit":10}`, "GREP_RESULT:", hasToolResult, input)

	case "compare-broad-grep":
		return toolCallAndReturn("grep_search", `{"pattern":"error|handling","include":"*.go","limit":10}`, "GREP_RESULT:", hasToolResult, input)

	case "compare-symbol-grep":
		return toolCallAndReturn("grep_search", `{"pattern":"DomainError","limit":10}`, "GREP_RESULT:", hasToolResult, input)

	case "compare-cross-grep":
		return toolCallAndReturn("grep_search", `{"pattern":"http.*handler|handler.*request","include":"*.go","ignore_case":true,"limit":10}`, "GREP_RESULT:", hasToolResult, input)

	case "smart-search-empty-query":
		toolCount := countToolResults(input)
		if toolCount == 0 {
			return toolCallMessage("smart_search", `{"query":"","limit":10}`), nil
		}
		return textMessage(fmt.Sprintf("HANDLED_ERROR:%s", extractLastToolResult(input))), nil

	case "grep-no-duplicate":
		return toolCallAndReturn("grep_search", `{"pattern":"ListenAndServe","limit":10}`, "GREP_RESULT:", hasToolResult, input)

	case "glob-no-duplicate":
		return toolCallAndReturn("glob", `{"pattern":"**/*.go","limit":10}`, "GLOB_RESULT:", hasToolResult, input)

	case "lsp-definition":
		return toolCallAndReturn("lsp", `{"symbol_name":"TestFunc","operation":"definition"}`, "LSP_RESULT:", hasToolResult, input)

	case "lsp-references":
		return toolCallAndReturn("lsp", `{"symbol_name":"HandleError","operation":"references"}`, "LSP_REFS:", hasToolResult, input)

	case "lsp-implementation":
		return toolCallAndReturn("lsp", `{"symbol_name":"Repository","operation":"implementation"}`, "LSP_IMPL:", hasToolResult, input)

	case "lsp-invalid-op":
		return toolCallAndReturn("lsp", `{"symbol_name":"Foo","operation":"hover"}`, "LSP_ERR:", hasToolResult, input)

	case "lsp-missing-symbol":
		return toolCallAndReturn("lsp", `{"symbol_name":"NonExistentSymbol12345","operation":"definition"}`, "LSP_MISS:", hasToolResult, input)

	case "lsp-multilang":
		toolCount := countToolResults(input)
		switch {
		case toolCount == 0:
			return toolCallMessageWithID("lsp", `{"symbol_name":"ProcessData","operation":"definition"}`, "call_mock_1"), nil
		case toolCount == 1:
			return toolCallMessageWithID("lsp", `{"symbol_name":"UserService","operation":"definition"}`, "call_mock_2"), nil
		case toolCount == 2:
			return toolCallMessageWithID("lsp", `{"symbol_name":"DataProcessor","operation":"definition"}`, "call_mock_3"), nil
		case toolCount == 3:
			return toolCallMessageWithID("lsp", `{"symbol_name":"Config","operation":"definition"}`, "call_mock_4"), nil
		default:
			results := collectAllToolResults(input)
			return textMessage(fmt.Sprintf("MULTILANG_RESULTS:[%s]", strings.Join(results, "|"))), nil
		}

	case "write-file-go-error":
		if hasToolResult {
			return textMessage(fmt.Sprintf("WRITE_RESULT:%s", extractLastToolResult(input))), nil
		}
		return toolCallMessage("write_file", `{"file_path":"broken.go","content":"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tx := undefinedVar\n\tfmt.Println(x)\n}\n"}`), nil

	case "lsp-symbol-search":
		toolCount := countToolResults(input)
		switch {
		case toolCount == 0:
			return toolCallMessageWithID("lsp", `{"symbol_name":"greet","operation":"definition"}`, "call_mock_1"), nil
		case toolCount == 1:
			return toolCallMessageWithID("lsp", `{"symbol_name":"Calculator","operation":"definition"}`, "call_mock_2"), nil
		default:
			results := collectAllToolResults(input)
			return textMessage(fmt.Sprintf("SYMBOL_SEARCH:[%s]", strings.Join(results, "|"))), nil
		}

	case "agent-failure":
		if isCodeAgentCall(input) {
			return nil, fmt.Errorf("mock code agent error: file not found")
		}
		if hasToolResult {
			return textMessage("Agent failed. Handling error."), nil
		}
		return toolCallMessage("spawn_agent", `{"action":"spawn","subtask_id":"test-subtask-1"}`), nil

	case "multi-agent-read":
		if isCodeAgentCall(input) {
			if hasToolResult {
				return textMessage(fmt.Sprintf("Code agent read file: %s", extractLastToolResult(input))), nil
			}
			return toolCallMessage("read_file", `{"file_path":"src/main.ts"}`), nil
		}
		if hasToolResult {
			return textMessage("Agent read the file successfully."), nil
		}
		return toolCallMessage("spawn_agent", `{"action":"spawn","subtask_id":"test-subtask-1"}`), nil

	case "persistent-shell":
		toolCount := countToolResults(input)
		switch {
		case toolCount == 0:
			return toolCallMessage("execute_command", `{"command":"cd /tmp"}`), nil
		case toolCount == 1:
			return toolCallMessageWithID("execute_command", `{"command":"pwd"}`, "call_mock_2"), nil
		default:
			results := collectAllToolResults(input)
			return textMessage(fmt.Sprintf("PERSISTENT_SHELL_RESULTS:[%s]", strings.Join(results, "|"))), nil
		}

	case "background-process":
		toolCount := countToolResults(input)
		switch {
		case toolCount == 0:
			return toolCallMessage("execute_command", `{"command":"tail -f /dev/null","background":true}`), nil
		case toolCount == 1:
			return toolCallMessageWithID("execute_command", `{"bg_action":"list"}`, "call_mock_2"), nil
		case toolCount == 2:
			return toolCallMessageWithID("execute_command", `{"bg_action":"kill","bg_id":"bg-1"}`, "call_mock_3"), nil
		default:
			results := collectAllToolResults(input)
			return textMessage(fmt.Sprintf("BACKGROUND_RESULTS:[%s]", strings.Join(results, "|"))), nil
		}

	case "cancel-during-stream":
		return textMessage("This is a response that will be cancelled."), nil

	case "parallel-exec":
		toolCount := countToolResults(input)
		if toolCount >= 2 {
			results := collectAllToolResults(input)
			return textMessage(fmt.Sprintf("PARALLEL_RESULTS:[%s]", strings.Join(results, "|"))), nil
		}
		msg := &schema.Message{
			Role: schema.Assistant,
			ToolCalls: []schema.ToolCall{
				{
					ID:   "call_par_1",
					Type: "function",
					Function: schema.FunctionCall{
						Name:      "execute_command",
						Arguments: `{"command":"echo parallel_a"}`,
					},
				},
				{
					ID:   "call_par_2",
					Type: "function",
					Function: schema.FunctionCall{
						Name:      "execute_command",
						Arguments: `{"command":"echo parallel_b"}`,
					},
				},
			},
			ResponseMeta: &schema.ResponseMeta{
				FinishReason: "tool_calls",
			},
		}
		return msg, nil

	// --- Integration test scenarios (tools execute on real filesystem via LocalClientOperationsProxy) ---

	case "local-read":
		// Reads test.txt from project root
		if hasToolResult {
			return textMessage(fmt.Sprintf("FILE_CONTENT:%s", extractLastToolResult(input))), nil
		}
		return toolCallMessage("read_file", `{"file_path":"test.txt"}`), nil

	case "local-write":
		// Writes output.txt to project root
		if hasToolResult {
			return textMessage(fmt.Sprintf("WRITE_DONE:%s", extractLastToolResult(input))), nil
		}
		return toolCallMessage("write_file", `{"file_path":"output.txt","content":"hello from agent"}`), nil

	case "local-edit":
		// Edits app.txt: replaces "old_value" with "new_value"
		if hasToolResult {
			return textMessage(fmt.Sprintf("EDIT_DONE:%s", extractLastToolResult(input))), nil
		}
		return toolCallMessage("edit_file", `{"file_path":"app.txt","old_string":"old_value","new_string":"new_value"}`), nil

	case "local-exec":
		// Runs echo command
		if hasToolResult {
			return textMessage(fmt.Sprintf("EXEC_RESULT:%s", extractLastToolResult(input))), nil
		}
		return toolCallMessage("execute_command", `{"command":"echo hello_from_test"}`), nil

	case "local-multi-tool":
		// Chain: read a.txt → read b.txt → answer with both contents
		toolCount := countToolResults(input)
		switch {
		case toolCount == 0:
			return toolCallMessage("read_file", `{"file_path":"a.txt"}`), nil
		case toolCount == 1:
			return toolCallMessageWithID("read_file", `{"file_path":"b.txt"}`, "call_mock_2"), nil
		default:
			results := collectAllToolResults(input)
			return textMessage(fmt.Sprintf("MULTI_READ:[%s]", strings.Join(results, "|"))), nil
		}

	case "local-read-error":
		// First: try to read nonexistent file; second: recover with answer
		toolCount := countToolResults(input)
		if toolCount == 0 {
			return toolCallMessage("read_file", `{"file_path":"nonexistent.txt"}`), nil
		}
		lastResult := extractLastToolResult(input)
		if strings.Contains(lastResult, "not found") || strings.Contains(lastResult, "no such") || strings.Contains(lastResult, "cannot find") {
			return textMessage(fmt.Sprintf("RECOVERED:%s", lastResult)), nil
		}
		return textMessage(fmt.Sprintf("UNEXPECTED:%s", lastResult)), nil

	case "local-glob-grep":
		// Step 1: glob for *.txt, Step 2: grep for "hello", Step 3: answer
		toolCount := countToolResults(input)
		switch {
		case toolCount == 0:
			return toolCallMessage("glob", `{"pattern":"**/*.txt"}`), nil
		case toolCount == 1:
			return toolCallMessageWithID("grep_search", `{"pattern":"hello","limit":10}`, "call_mock_2"), nil
		default:
			results := collectAllToolResults(input)
			return textMessage(fmt.Sprintf("SEARCH_DONE:[%s]", strings.Join(results, "|"))), nil
		}

	default:
		return textMessage("Unknown scenario"), nil
	}
}

// Stream implements model.ChatModel.Stream
func (m *MockChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}

	sr, sw := schema.Pipe[*schema.Message](1)
	go func() {
		defer sw.Close()

		if m.scenario == "cancel-during-stream" {
			select {
			case <-time.After(3 * time.Second):
			case <-ctx.Done():
				return
			}
		}

		sw.Send(msg, nil)
	}()

	return sr, nil
}

// WithTools implements model.ToolCallingChatModel.WithTools
func (m *MockChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

// BindTools implements model.ChatModel.BindTools (deprecated)
func (m *MockChatModel) BindTools(tools []*schema.ToolInfo) error {
	return nil
}

// Helper functions

// textMessage creates an assistant message with text content.
func textMessage(content string) *schema.Message {
	return &schema.Message{
		Role:    schema.Assistant,
		Content: content,
		ResponseMeta: &schema.ResponseMeta{
			FinishReason: "stop",
		},
	}
}

// toolCallMessage creates an assistant message with a single tool call.
func toolCallMessage(toolName, args string) *schema.Message {
	return &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{{
			ID:   "call_mock_1",
			Type: "function",
			Function: schema.FunctionCall{
				Name:      toolName,
				Arguments: args,
			},
		}},
		ResponseMeta: &schema.ResponseMeta{
			FinishReason: "tool_calls",
		},
	}
}

// reasoningMessage creates an assistant message with reasoning content and an answer.
func reasoningMessage(thinking, answer string) *schema.Message {
	return &schema.Message{
		Role:             schema.Assistant,
		Content:          answer,
		ReasoningContent: thinking,
		ResponseMeta: &schema.ResponseMeta{
			FinishReason: "stop",
		},
	}
}

// containsToolResult checks if any message in input has Tool role.
func containsToolResult(input []*schema.Message) bool {
	for _, msg := range input {
		if msg.Role == schema.Tool {
			return true
		}
	}
	return false
}

// extractLastToolResult returns the Content of the last Tool-role message.
func extractLastToolResult(input []*schema.Message) string {
	for i := len(input) - 1; i >= 0; i-- {
		if input[i].Role == schema.Tool {
			return input[i].Content
		}
	}
	return ""
}

// countToolResults counts how many Tool-role messages are in input.
func countToolResults(input []*schema.Message) int {
	count := 0
	for _, msg := range input {
		if msg.Role == schema.Tool {
			count++
		}
	}
	return count
}

// isCodeAgentCall checks if the input contains a user message starting with "Subtask:".
func isCodeAgentCall(input []*schema.Message) bool {
	for _, msg := range input {
		if msg.Role == schema.User && (strings.HasPrefix(msg.Content, "Subtask:") || strings.Contains(msg.Content, "Subtask:")) {
			return true
		}
	}
	return false
}

// toolCallAndReturn is a helper for simple two-step scenarios:
// step 1: return tool call with given args
// step 2: return text with resultPrefix + last tool result
func toolCallAndReturn(toolName, args, resultPrefix string, hasToolResult bool, input []*schema.Message) (*schema.Message, error) {
	if hasToolResult {
		return textMessage(fmt.Sprintf("%s%s", resultPrefix, extractLastToolResult(input))), nil
	}
	return toolCallMessage(toolName, args), nil
}

// toolCallMessageWithID creates a tool call message with a specific call ID.
func toolCallMessageWithID(toolName, args, callID string) *schema.Message {
	return &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{{
			ID:   callID,
			Type: "function",
			Function: schema.FunctionCall{
				Name:      toolName,
				Arguments: args,
			},
		}},
		ResponseMeta: &schema.ResponseMeta{
			FinishReason: "tool_calls",
		},
	}
}

// collectAllToolResults returns the Content of every Tool-role message in input.
func collectAllToolResults(input []*schema.Message) []string {
	var results []string
	for _, msg := range input {
		if msg.Role == schema.Tool {
			results = append(results, msg.Content)
		}
	}
	return results
}
