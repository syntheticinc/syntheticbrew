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

// loopThreshold is the consecutive-call/error count at which a loop warning fires.
const loopThreshold = 3

// GetContextReminder returns a loop/error warning when the session is stuck, else
// nothing. Priority 98 — appears last in context for maximum recency bias.
//
// Cache-stability invariant: the emitted text must be byte-identical every step it
// persists, so the trailing-nudge dedup in MessageModifier treats it as one
// append-once item. Therefore the text is COUNT-FREE (no climbing call/error number)
// and the routine per-step "you called X(xN)" summary is gone — it was redundant
// with the transcript and changed every step, discarding the provider's prompt cache.
func (r *ToolCallHistoryReminder) GetContextReminder(_ context.Context, sessionID string) (string, int, bool) {
	if sessionID == "" {
		return "", 0, false
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	var warnings []string

	// Same-tool loop: count-free, stable text so it dedups to one append.
	if r.consecutiveSameTool[sessionID] >= loopThreshold {
		lastTool := r.lastToolName[sessionID]
		warnings = append(warnings, fmt.Sprintf("⚠️ WARNING: You appear to be calling \"%s\" repeatedly. You may be stuck in a loop. Try a DIFFERENT tool or approach. If waiting for subtasks — use spawn_agent to start them.", lastTool))
	}

	// Error loops: count-free, stable text. Sorted for deterministic order so the
	// concatenation is byte-identical across calls when the same tools loop.
	errorLoops := r.consecutiveErrors[sessionID]
	if len(errorLoops) > 0 {
		loopedTools := make([]string, 0, len(errorLoops))
		for toolName, count := range errorLoops {
			if count >= loopThreshold {
				loopedTools = append(loopedTools, toolName)
			}
		}
		sort.Strings(loopedTools)
		for _, toolName := range loopedTools {
			warnings = append(warnings, fmt.Sprintf("⚠️ WARNING: Tool \"%s\" keeps returning [ERROR]. You are STUCK in a loop. STOP calling this tool the same way. Read the error message and FIX your arguments, or try a DIFFERENT approach.", toolName))
		}
	}

	if len(warnings) == 0 {
		return "", 0, false
	}

	return strings.Join(warnings, "\n\n"), 98, true
}
