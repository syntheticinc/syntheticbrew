package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// ToolCallRecorder defines interface for recording tool calls and results.
// Consumer-side interface: defined here where it's used.
type ToolCallRecorder interface {
	RecordToolCall(sessionID, toolName string)
	RecordToolResult(sessionID, toolName, result string)
}

// ToolCallHistoryReminder tracks tool calls per session and reminds the agent
// to avoid redundant calls. Implements ContextReminderProvider.
type ToolCallHistoryReminder struct {
	mu                  sync.Mutex
	callsPerTool        map[string]map[string]int  // sessionID -> toolName -> count
	consecutiveErrors   map[string]map[string]int  // sessionID -> toolName -> consecutive error count
	lastToolResult      map[string]map[string]bool // sessionID -> toolName -> was last result an error?
	lastToolName        map[string]string          // sessionID -> last tool name called
	consecutiveSameTool map[string]int             // sessionID -> consecutive same-tool call count
}

// NewToolCallHistoryReminder creates a new ToolCallHistoryReminder
func NewToolCallHistoryReminder() *ToolCallHistoryReminder {
	return &ToolCallHistoryReminder{
		callsPerTool:        make(map[string]map[string]int),
		consecutiveErrors:   make(map[string]map[string]int),
		lastToolResult:      make(map[string]map[string]bool),
		lastToolName:        make(map[string]string),
		consecutiveSameTool: make(map[string]int),
	}
}

// RecordToolCall records a tool call for a session
func (r *ToolCallHistoryReminder) RecordToolCall(sessionID, toolName string) {
	if sessionID == "" || toolName == "" {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.callsPerTool[sessionID] == nil {
		r.callsPerTool[sessionID] = make(map[string]int)
	}
	r.callsPerTool[sessionID][toolName]++

	// Track consecutive same-tool calls
	if r.lastToolName[sessionID] == toolName {
		r.consecutiveSameTool[sessionID]++
	} else {
		r.consecutiveSameTool[sessionID] = 1
		r.lastToolName[sessionID] = toolName
	}
}

// RecordToolResult records a tool result and detects error loops
func (r *ToolCallHistoryReminder) RecordToolResult(sessionID, toolName, result string) {
	if sessionID == "" || toolName == "" || result == "" {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Initialize maps if needed
	if r.consecutiveErrors[sessionID] == nil {
		r.consecutiveErrors[sessionID] = make(map[string]int)
	}
	if r.lastToolResult[sessionID] == nil {
		r.lastToolResult[sessionID] = make(map[string]bool)
	}

	// Check if result is an error
	isError := strings.HasPrefix(result, "[ERROR]")

	// Update consecutive error count
	if isError {
		if r.lastToolResult[sessionID][toolName] {
			// Last was also an error → increment
			r.consecutiveErrors[sessionID][toolName]++
		} else {
			// First error in sequence
			r.consecutiveErrors[sessionID][toolName] = 1
		}
	} else {
		// Success → reset
		r.consecutiveErrors[sessionID][toolName] = 0
	}

	// Update last result state
	r.lastToolResult[sessionID][toolName] = isError
}

// ClearSession removes all tool call history for a session
func (r *ToolCallHistoryReminder) ClearSession(sessionID string) {
	if sessionID == "" {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.callsPerTool, sessionID)
	delete(r.consecutiveErrors, sessionID)
	delete(r.lastToolResult, sessionID)
	delete(r.lastToolName, sessionID)
	delete(r.consecutiveSameTool, sessionID)
}

// GetContextReminder returns a reminder if the session has >= 3 tool calls.
// Priority: 98 — appears last in context (after work context=90, environment=95) for maximum recency bias.
func (r *ToolCallHistoryReminder) GetContextReminder(_ context.Context, sessionID string) (string, int, bool) {
	if sessionID == "" {
		return "", 0, false
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	toolCalls := r.callsPerTool[sessionID]
	if len(toolCalls) == 0 {
		return "", 0, false
	}

	// Count total calls
	totalCalls := 0
	for _, count := range toolCalls {
		totalCalls += count
	}

	// Only show reminder if >= 3 tool calls
	if totalCalls < 3 {
		return "", 0, false
	}

	// Build summary: "read_file(x3), smart_search(x2)"
	var parts []string
	// Sort tool names for consistent output
	toolNames := make([]string, 0, len(toolCalls))
	for name := range toolCalls {
		toolNames = append(toolNames, name)
	}
	sort.Strings(toolNames)

	for _, name := range toolNames {
		count := toolCalls[name]
		if count > 1 {
			parts = append(parts, fmt.Sprintf("%s(x%d)", name, count))
		} else {
			parts = append(parts, name)
		}
	}

	reminder := fmt.Sprintf("**TOOL HISTORY:** You called: %s.\nResults are in conversation above. Re-read only if file was modified since last read.", strings.Join(parts, ", "))

	// Check for consecutive same-tool calls
	if sameToolCount := r.consecutiveSameTool[sessionID]; sameToolCount >= 3 {
		lastTool := r.lastToolName[sessionID]
		reminder += fmt.Sprintf("\n\n⚠️ WARNING: You called \"%s\" %d times in a row. You may be stuck in a loop. Try a DIFFERENT tool or approach. If waiting for subtasks — use spawn_agent to start them.", lastTool, sameToolCount)
	}

	// Check for error loops
	errorLoops := r.consecutiveErrors[sessionID]
	for toolName, count := range errorLoops {
		if count >= 3 {
			reminder += fmt.Sprintf("\n\n⚠️ WARNING: Tool \"%s\" returned [ERROR] %d times in a row. You are STUCK in a loop. STOP calling this tool the same way. Read the error message and FIX your arguments, or try a DIFFERENT approach.", toolName, count)
		}
	}

	return reminder, 98, true
}
