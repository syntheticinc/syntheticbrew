package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudwego/eino/schema"
)

// ContextLogger logs the composition of context for debugging.
//
// Production-deployment note: when ContextLogPath is configured but the
// underlying filesystem is read-only (typical in k8s pods without a
// writable volume mounted at logs/), every mkdir attempt fails. To
// avoid flooding ERROR-level output on every chat turn, the logger
// performs a sticky one-shot disable: the first mkdir failure logs a
// single INFO line and sets `disabled`; all subsequent calls return
// early without logging until the process restarts.
type ContextLogger struct {
	logDir           string
	sessionID        string
	sessionDirName   string // timestamp-based directory name for easier finding
	maxContextTokens int
	agentID          string // "supervisor" | "code-agent-xxx"
	parentAgentID    string // parent agent ID (for Code Agents → "supervisor")
	taskID           string // task being executed (for Code Agents)

	// Sticky-disable state: set once on first mkdir failure to silence
	// repeated ERROR logs from a read-only logs/ filesystem in prod.
	disabled    atomic.Bool
	disableOnce sync.Once
}

// ensureDir is the single chokepoint for creating the session log
// directory. On first failure it logs an INFO line via disableOnce and
// flips the sticky-disable flag; subsequent calls return false silently
// so callers can short-circuit without spamming the log.
//
// Returns true when the directory is ready and the caller may proceed
// to write files inside it; false when logging is disabled for this
// logger instance (caller must early-return).
func (cl *ContextLogger) ensureDir(ctx context.Context, sessionDir string) bool {
	if cl.disabled.Load() {
		return false
	}
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		cl.disableOnce.Do(func() {
			slog.InfoContext(ctx, "context logging disabled",
				"reason", "mkdir failed",
				"error", err,
				"path", sessionDir,
				"hint", "set ContextLogPath to empty string or mount a writable volume to silence this")
			cl.disabled.Store(true)
		})
		return false
	}
	return true
}

// generateSessionDirName creates a timestamp-based directory name
// Format: YYYY-MM-DD_HH-MM-SS_<session-prefix>
func generateSessionDirName(sessionID string) string {
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	// Use first 8 chars of sessionID as suffix for uniqueness
	sessionPrefix := sessionID
	if len(sessionPrefix) > 8 {
		sessionPrefix = sessionPrefix[:8]
	}
	return fmt.Sprintf("%s_%s", timestamp, sessionPrefix)
}

// NewContextLogger creates a new context logger with session directory
func NewContextLogger(logDir, sessionID string) *ContextLogger {
	return &ContextLogger{
		logDir:           logDir,
		sessionID:        sessionID,
		sessionDirName:   generateSessionDirName(sessionID),
		maxContextTokens: 16000, // default
		agentID:          "supervisor",
	}
}

// NewContextLoggerWithLimit creates a new context logger with custom token limit
func NewContextLoggerWithLimit(logDir, sessionID string, maxContextTokens int) *ContextLogger {
	return &ContextLogger{
		logDir:           logDir,
		sessionID:        sessionID,
		sessionDirName:   generateSessionDirName(sessionID),
		maxContextTokens: maxContextTokens,
		agentID:          "supervisor",
	}
}

// NewContextLoggerForAgent creates a context logger for a specific agent
func NewContextLoggerForAgent(logDir, sessionID, agentID, parentAgentID, taskID string, maxContextTokens int) *ContextLogger {
	if maxContextTokens <= 0 {
		maxContextTokens = 16000
	}
	return &ContextLogger{
		logDir:           logDir,
		sessionID:        sessionID,
		sessionDirName:   generateSessionDirName(sessionID),
		maxContextTokens: maxContextTokens,
		agentID:          agentID,
		parentAgentID:    parentAgentID,
		taskID:           taskID,
	}
}

// GetSessionDirName returns the session directory name
func (cl *ContextLogger) GetSessionDirName() string {
	return cl.sessionDirName
}

// SetSessionDirName sets the session directory name (to share with parent logger)
func (cl *ContextLogger) SetSessionDirName(dirName string) {
	cl.sessionDirName = dirName
}

// GetAgentID returns the agent ID
func (cl *ContextLogger) GetAgentID() string {
	return cl.agentID
}

// LogContext logs the current context composition to a step-specific JSON file
func (cl *ContextLogger) LogContext(ctx context.Context, messages []*schema.Message, step int) {
	slog.DebugContext(ctx, "LogContext called", "log_dir", cl.logDir, "session_id", cl.sessionID, "step", step, "messages_count", len(messages))
	if cl.logDir == "" || cl.sessionID == "" {
		slog.WarnContext(ctx, "LogContext skipped: logDir or sessionID is empty", "logDir", cl.logDir, "sessionID", cl.sessionID)
		return
	}

	// Create session directory if it doesn't exist
	sessionDir := filepath.Join(cl.logDir, cl.sessionDirName)
	if !cl.ensureDir(ctx, sessionDir) {
		return
	}
	slog.DebugContext(ctx, "session directory created", "path", sessionDir)

	// Create context snapshot
	snapshot := ContextSnapshot{
		Timestamp:     time.Now().Format(time.RFC3339),
		Step:          step,
		AgentID:       cl.agentID,
		ParentAgentID: cl.parentAgentID,
		TaskID:        cl.taskID,
		TotalMessages: len(messages),
		TotalChars:    0,
		TotalTokens:   0,
		MaxTokens:     cl.maxContextTokens,
		Messages:      make([]MessageInfo, 0, len(messages)),
	}

	// Analyze each message
	for i, msg := range messages {
		chars := len(msg.Content)
		tokens := charsToTokens(chars)

		msgInfo := MessageInfo{
			Index:          i,
			Role:           string(msg.Role),
			Chars:          chars,
			Tokens:         tokens,
			Content:        truncateString(msg.Content, 500), // Truncate to avoid memory bloat in logs
			ContentPreview: truncateString(msg.Content, 200),
		}

		snapshot.TotalChars += chars
		snapshot.TotalTokens += tokens

		// Add extra info for tool messages
		if msg.Role == schema.Tool {
			msgInfo.ToolName = msg.Name
			msgInfo.ToolCallID = msg.ToolCallID
		}

		// Add extra info for assistant messages with tool calls
		if msg.Role == schema.Assistant && len(msg.ToolCalls) > 0 {
			msgInfo.ToolCalls = make([]ToolCallInfo, 0, len(msg.ToolCalls))
			for idx, tc := range msg.ToolCalls {
				// Use ID if available (OpenAI), otherwise generate from Index (Ollama),
				// or fall back to array position (Ollama without Index)
				toolCallID := tc.ID
				if toolCallID == "" {
					if tc.Index != nil {
						toolCallID = fmt.Sprintf("call_%d_%s", *tc.Index, tc.Function.Name)
					} else {
						// Ollama doesn't provide ID or Index - use array position
						toolCallID = fmt.Sprintf("call_%d_%s", idx, tc.Function.Name)
					}
				}
				msgInfo.ToolCalls = append(msgInfo.ToolCalls, ToolCallInfo{
					ID:        toolCallID,
					Name:      tc.Function.Name,
					Index:     tc.Index,
					Arguments: tc.Function.Arguments,
				})
			}
		}

		snapshot.Messages = append(snapshot.Messages, msgInfo)
	}

	// Calculate usage
	snapshot.RemainingTokens = cl.maxContextTokens - snapshot.TotalTokens
	if snapshot.RemainingTokens < 0 {
		snapshot.RemainingTokens = 0
	}
	if cl.maxContextTokens > 0 {
		snapshot.UsedPercent = float64(snapshot.TotalTokens) / float64(cl.maxContextTokens) * 100
	}

	// Calculate statistics
	toolCount := 0
	userCount := 0
	assistantCount := 0
	systemCount := 0

	for _, msg := range snapshot.Messages {
		switch msg.Role {
		case "tool":
			toolCount++
		case "user":
			userCount++
		case "assistant":
			assistantCount++
		case "system":
			systemCount++
		}
	}

	averageMsgTokens := 0
	if len(messages) > 0 {
		averageMsgTokens = snapshot.TotalTokens / len(messages)
	}

	snapshot.Statistics = ContextStatistics{
		SystemCount:      systemCount,
		UserCount:        userCount,
		AssistantCount:   assistantCount,
		ToolCount:        toolCount,
		AverageMsgTokens: averageMsgTokens,
	}

	// Write to step-specific JSON file
	cl.writeSnapshot(sessionDir, step, snapshot)
}

// ContextSnapshot represents a snapshot of the context
type ContextSnapshot struct {
	Timestamp       string            `json:"timestamp"`
	Step            int               `json:"step"`
	AgentID         string            `json:"agent_id,omitempty"`
	ParentAgentID   string            `json:"parent_agent_id,omitempty"`
	TaskID          string            `json:"task_id,omitempty"`
	TotalMessages   int               `json:"total_messages"`
	TotalChars      int               `json:"total_chars"`
	TotalTokens     int               `json:"total_tokens"`
	MaxTokens       int               `json:"max_tokens"`
	UsedPercent     float64           `json:"used_percent"`
	RemainingTokens int               `json:"remaining_tokens"`
	Messages        []MessageInfo     `json:"messages"`
	Statistics      ContextStatistics `json:"statistics"`
}

// MessageInfo represents information about a message
type MessageInfo struct {
	Index          int            `json:"index"`
	Role           string         `json:"role"`
	Chars          int            `json:"chars"`
	Tokens         int            `json:"tokens"`
	Content        string         `json:"content"`
	ContentPreview string         `json:"content_preview"`
	ToolName       string         `json:"tool_name,omitempty"`
	ToolCallID     string         `json:"tool_call_id,omitempty"`
	ToolCalls      []ToolCallInfo `json:"tool_calls,omitempty"`
}

// ToolCallInfo represents information about a tool call
type ToolCallInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Index     *int   `json:"index,omitempty"`     // Ollama uses Index instead of ID
	Arguments string `json:"arguments,omitempty"` // tool call arguments JSON
}

// ContextStatistics represents statistics about the context
type ContextStatistics struct {
	SystemCount      int `json:"system_count"`
	UserCount        int `json:"user_count"`
	AssistantCount   int `json:"assistant_count"`
	ToolCount        int `json:"tool_count"`
	AverageMsgTokens int `json:"average_msg_tokens"`
}

// writeSnapshot writes the context snapshot to a step-specific JSON file.
// Called from a background goroutine; no request context available — uses
// context.Background() for slog calls (acceptable since this is process-level
// background logging, not per-tenant work).
func (cl *ContextLogger) writeSnapshot(sessionDir string, step int, snapshot ContextSnapshot) {
	ctx := context.Background()
	// Create step-specific filename with agent prefix
	prefix := cl.agentID
	if prefix == "" {
		prefix = "supervisor"
	}
	filename := fmt.Sprintf("%s_step_%d_context.json", prefix, step)
	filepath := filepath.Join(sessionDir, filename)

	// Write snapshot as JSON file
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		slog.ErrorContext(ctx, "failed to marshal context snapshot", "error", err)
		return
	}

	if err := os.WriteFile(filepath, data, 0644); err != nil {
		slog.ErrorContext(ctx, "failed to write context snapshot", "error", err, "path", filepath)
		return
	}

	slog.InfoContext(ctx, "context snapshot saved",
		"step", step,
		"path", filepath,
		"messages", snapshot.TotalMessages,
		"tokens", snapshot.TotalTokens,
		"max_tokens", snapshot.MaxTokens,
		"used_percent", fmt.Sprintf("%.1f%%", snapshot.UsedPercent),
		"remaining_tokens", snapshot.RemainingTokens)
}

// truncateString truncates a string to max length
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// LogContextSummary logs a summary of the context to a summary file
func (cl *ContextLogger) LogContextSummary(ctx context.Context, messages []*schema.Message) {
	if cl.logDir == "" || cl.sessionID == "" {
		return
	}

	// Create session directory if it doesn't exist
	sessionDir := filepath.Join(cl.logDir, cl.sessionDirName)
	if !cl.ensureDir(ctx, sessionDir) {
		return
	}

	// Calculate summary
	summary := fmt.Sprintf("Context Summary: %d messages\n", len(messages))

	roleCounts := make(map[string]int)
	totalChars := 0
	for _, msg := range messages {
		roleCounts[string(msg.Role)]++
		totalChars += len(msg.Content)
	}
	totalTokens := charsToTokens(totalChars)
	remainingTokens := cl.maxContextTokens - totalTokens
	if remainingTokens < 0 {
		remainingTokens = 0
	}
	usedPercent := float64(0)
	if cl.maxContextTokens > 0 {
		usedPercent = float64(totalTokens) / float64(cl.maxContextTokens) * 100
	}

	for role, count := range roleCounts {
		summary += fmt.Sprintf("  %s: %d\n", role, count)
	}
	summary += fmt.Sprintf("\nToken usage:\n")
	summary += fmt.Sprintf("  Total: %d tokens (%.1f%% used)\n", totalTokens, usedPercent)
	summary += fmt.Sprintf("  Limit: %d tokens\n", cl.maxContextTokens)
	summary += fmt.Sprintf("  Remaining: %d tokens\n", remainingTokens)

	// Write summary file with agent prefix
	prefix := cl.agentID
	if prefix == "" {
		prefix = "supervisor"
	}
	summaryPath := filepath.Join(sessionDir, prefix+"_context_summary.txt")
	if err := os.WriteFile(summaryPath, []byte(summary), 0644); err != nil {
		slog.ErrorContext(ctx, "failed to write context summary", "error", err, "path", summaryPath)
		return
	}

	slog.InfoContext(ctx, "context summary saved", "path", summaryPath)
}

// LogCompressionReport logs a report about context compression
func (cl *ContextLogger) LogCompressionReport(ctx context.Context, beforeCount, afterCount int, removedToolResults []string) {
	if cl.logDir == "" || cl.sessionID == "" {
		return
	}

	// Create session directory if it doesn't exist
	sessionDir := filepath.Join(cl.logDir, cl.sessionDirName)
	if !cl.ensureDir(ctx, sessionDir) {
		return
	}

	// Create compression report
	report := fmt.Sprintf("Context Compression Report\n")
	report += fmt.Sprintf("=========================\n")
	report += fmt.Sprintf("Messages before compression: %d\n", beforeCount)
	report += fmt.Sprintf("Messages after compression: %d\n", afterCount)
	report += fmt.Sprintf("Messages removed: %d\n", beforeCount-afterCount)

	if len(removedToolResults) > 0 {
		report += fmt.Sprintf("\nRemoved tool results (%d):\n", len(removedToolResults))
		for _, toolName := range removedToolResults {
			report += fmt.Sprintf("  - %s\n", toolName)
		}
	} else {
		report += fmt.Sprintf("\nNo tool results were removed.\n")
	}

	// Write report file
	reportPath := filepath.Join(sessionDir, "context_compression_report.txt")
	if err := os.WriteFile(reportPath, []byte(report), 0644); err != nil {
		slog.ErrorContext(ctx, "failed to write compression report", "error", err, "path", reportPath)
		return
	}

	slog.InfoContext(ctx, "compression report saved", "path", reportPath, "removed", len(removedToolResults))
}

// SessionOverview represents the session structure for logging
type SessionOverview struct {
	SessionID  string             `json:"session_id"`
	StartedAt  string             `json:"started_at"`
	Supervisor SessionAgentInfo   `json:"supervisor"`
	CodeAgents []SessionAgentInfo `json:"code_agents"`
}

// SessionAgentInfo represents an agent in the session overview
type SessionAgentInfo struct {
	AgentID     string `json:"agent_id"`
	TaskID      string `json:"task_id,omitempty"`
	TaskTitle   string `json:"task_title,omitempty"`
	Parent      string `json:"parent,omitempty"`
	Status      string `json:"status"`
	TotalSteps  int    `json:"total_steps"`
	StartedAt   string `json:"started_at,omitempty"`
	CompletedAt string `json:"completed_at,omitempty"`
}

// LogSessionOverview writes a session_overview.json with agent relationships
func (cl *ContextLogger) LogSessionOverview(overview SessionOverview) {
	if cl.logDir == "" || cl.sessionID == "" {
		return
	}

	sessionDir := filepath.Join(cl.logDir, cl.sessionDirName)
	bgCtx := context.Background()
	if !cl.ensureDir(bgCtx, sessionDir) {
		return
	}

	data, err := json.MarshalIndent(overview, "", "  ")
	if err != nil {
		slog.ErrorContext(bgCtx, "failed to marshal session overview", "error", err)
		return
	}

	overviewPath := filepath.Join(sessionDir, "session_overview.json")
	if err := os.WriteFile(overviewPath, data, 0644); err != nil {
		slog.ErrorContext(bgCtx, "failed to write session overview", "error", err, "path", overviewPath)
		return
	}

	slog.InfoContext(bgCtx, "session overview saved", "path", overviewPath, "code_agents", len(overview.CodeAgents))
}
