package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
)

func TestNoReminder_FewCalls(t *testing.T) {
	r := NewToolCallHistoryReminder()

	// 0 calls → no reminder
	_, _, ok := r.GetContextReminder(context.Background(), "session-1")
	if ok {
		t.Error("expected no reminder with 0 calls")
	}

	// 1 call → no reminder
	r.RecordToolCall("session-1", "read_file")
	_, _, ok = r.GetContextReminder(context.Background(), "session-1")
	if ok {
		t.Error("expected no reminder with 1 call")
	}

	// 2 calls → no reminder
	r.RecordToolCall("session-1", "search_code")
	_, _, ok = r.GetContextReminder(context.Background(), "session-1")
	if ok {
		t.Error("expected no reminder with 2 calls")
	}
}

func TestReminder_MultipleCalls(t *testing.T) {
	r := NewToolCallHistoryReminder()
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "search_code")
	r.RecordToolCall("session-1", "smart_search")

	content, priority, ok := r.GetContextReminder(context.Background(), "session-1")
	if !ok {
		t.Fatal("expected reminder with 3 calls")
	}
	if priority != 98 {
		t.Errorf("priority = %d, want 98", priority)
	}
	if !strings.Contains(content, "TOOL HISTORY") {
		t.Errorf("expected TOOL HISTORY in content: %s", content)
	}
	if !strings.Contains(content, "read_file") {
		t.Errorf("expected read_file in content: %s", content)
	}
	if !strings.Contains(content, "search_code") {
		t.Errorf("expected search_code in content: %s", content)
	}
	if !strings.Contains(content, "smart_search") {
		t.Errorf("expected smart_search in content: %s", content)
	}
}

func TestDuplicateCount(t *testing.T) {
	r := NewToolCallHistoryReminder()
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "read_file")

	content, _, ok := r.GetContextReminder(context.Background(), "session-1")
	if !ok {
		t.Fatal("expected reminder with 3 calls")
	}
	if !strings.Contains(content, "read_file(x3)") {
		t.Errorf("expected read_file(x3) in content: %s", content)
	}
}

func TestSingleCallNoCount(t *testing.T) {
	r := NewToolCallHistoryReminder()
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "smart_search")

	content, _, ok := r.GetContextReminder(context.Background(), "session-1")
	if !ok {
		t.Fatal("expected reminder")
	}
	// read_file called twice → shows (x2)
	if !strings.Contains(content, "read_file(x2)") {
		t.Errorf("expected read_file(x2): %s", content)
	}
	// smart_search called once → no (x1)
	if strings.Contains(content, "smart_search(x") {
		t.Errorf("expected smart_search without count suffix: %s", content)
	}
	if !strings.Contains(content, "smart_search") {
		t.Errorf("expected smart_search in content: %s", content)
	}
}

func TestClearSession(t *testing.T) {
	r := NewToolCallHistoryReminder()
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "read_file")

	// Verify reminder exists
	_, _, ok := r.GetContextReminder(context.Background(), "session-1")
	if !ok {
		t.Fatal("expected reminder before clear")
	}

	// Clear
	r.ClearSession("session-1")

	// Verify no reminder
	_, _, ok = r.GetContextReminder(context.Background(), "session-1")
	if ok {
		t.Error("expected no reminder after clear")
	}
}

func TestSessionIsolation(t *testing.T) {
	r := NewToolCallHistoryReminder()
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-2", "smart_search")

	// session-1 has 3 calls → reminder
	_, _, ok := r.GetContextReminder(context.Background(), "session-1")
	if !ok {
		t.Error("expected reminder for session-1")
	}

	// session-2 has 1 call → no reminder
	_, _, ok = r.GetContextReminder(context.Background(), "session-2")
	if ok {
		t.Error("expected no reminder for session-2")
	}
}

func TestEmptySessionID(t *testing.T) {
	r := NewToolCallHistoryReminder()

	// RecordToolCall with empty sessionID should be no-op
	r.RecordToolCall("", "read_file")

	_, _, ok := r.GetContextReminder(context.Background(), "")
	if ok {
		t.Error("expected no reminder for empty sessionID")
	}
}

func TestEmptyToolName(t *testing.T) {
	r := NewToolCallHistoryReminder()

	// RecordToolCall with empty toolName should be no-op
	r.RecordToolCall("session-1", "")
	r.RecordToolCall("session-1", "")
	r.RecordToolCall("session-1", "")

	_, _, ok := r.GetContextReminder(context.Background(), "session-1")
	if ok {
		t.Error("expected no reminder for empty tool names")
	}
}

func TestSortedToolNames(t *testing.T) {
	r := NewToolCallHistoryReminder()
	r.RecordToolCall("session-1", "write_file")
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "edit_file")

	content, _, ok := r.GetContextReminder(context.Background(), "session-1")
	if !ok {
		t.Fatal("expected reminder")
	}

	// Tool names should be sorted alphabetically
	editIdx := strings.Index(content, "edit_file")
	readIdx := strings.Index(content, "read_file")
	writeIdx := strings.Index(content, "write_file")

	if editIdx > readIdx || readIdx > writeIdx {
		t.Errorf("expected sorted order (edit_file, read_file, write_file): %s", content)
	}
}

func TestConcurrentAccess(t *testing.T) {
	r := NewToolCallHistoryReminder()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.RecordToolCall("session-1", "read_file")
		}()
	}

	// Concurrent reads
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.GetContextReminder(context.Background(), "session-1")
		}()
	}

	wg.Wait()

	// After 100 concurrent RecordToolCall, should have reminder
	content, _, ok := r.GetContextReminder(context.Background(), "session-1")
	if !ok {
		t.Fatal("expected reminder after concurrent writes")
	}
	if !strings.Contains(content, "read_file(x100)") {
		t.Errorf("expected read_file(x100): %s", content)
	}
}

func TestRecordToolResult_ErrorLoopDetection(t *testing.T) {
	r := NewToolCallHistoryReminder()

	// Setup: Need at least 3 tool calls for reminder to show
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "read_file")

	// Record 3 consecutive errors
	r.RecordToolResult("session-1", "read_file", "[ERROR] file not found")
	r.RecordToolResult("session-1", "read_file", "[ERROR] file not found")
	r.RecordToolResult("session-1", "read_file", "[ERROR] file not found")

	// Verify warning appears in reminder
	content, _, ok := r.GetContextReminder(context.Background(), "session-1")
	if !ok {
		t.Fatal("expected reminder")
	}
	if !strings.Contains(content, "⚠️ WARNING") {
		t.Errorf("expected warning after 3 consecutive errors: %s", content)
	}
	if !strings.Contains(content, "STUCK in a loop") {
		t.Errorf("expected 'STUCK in a loop' message: %s", content)
	}
	if !strings.Contains(content, "read_file") {
		t.Errorf("expected tool name in warning: %s", content)
	}
}

func TestRecordToolResult_ResetOnSuccess(t *testing.T) {
	r := NewToolCallHistoryReminder()

	// Setup: Need at least 3 tool calls for reminder to show
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "read_file")

	// Record 2 errors then success
	r.RecordToolResult("session-1", "read_file", "[ERROR] file not found")
	r.RecordToolResult("session-1", "read_file", "[ERROR] file not found")
	r.RecordToolResult("session-1", "read_file", "file contents here")

	// Verify no error-loop warning (count reset by success)
	// Note: consecutive same-tool warning may still appear since we called read_file 3x in a row
	content, _, ok := r.GetContextReminder(context.Background(), "session-1")
	if !ok {
		t.Fatal("expected reminder")
	}
	if strings.Contains(content, "STUCK in a loop") {
		t.Errorf("expected no error-loop warning after success: %s", content)
	}

	// Now record 3 more errors
	r.RecordToolResult("session-1", "read_file", "[ERROR] file not found")
	r.RecordToolResult("session-1", "read_file", "[ERROR] file not found")
	r.RecordToolResult("session-1", "read_file", "[ERROR] file not found")

	// Verify error-loop warning appears again
	content, _, ok = r.GetContextReminder(context.Background(), "session-1")
	if !ok {
		t.Fatal("expected reminder")
	}
	if !strings.Contains(content, "STUCK in a loop") {
		t.Errorf("expected error-loop warning after new error sequence: %s", content)
	}
}

func TestRecordToolResult_MixedTools(t *testing.T) {
	r := NewToolCallHistoryReminder()

	// Setup: Need at least 3 tool calls for reminder to show
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "write_file")
	r.RecordToolCall("session-1", "read_file")

	// read_file: 3 consecutive errors
	r.RecordToolResult("session-1", "read_file", "[ERROR] not found")
	r.RecordToolResult("session-1", "read_file", "[ERROR] not found")
	r.RecordToolResult("session-1", "read_file", "[ERROR] not found")

	// write_file: only 1 error
	r.RecordToolResult("session-1", "write_file", "[ERROR] permission denied")

	content, _, ok := r.GetContextReminder(context.Background(), "session-1")
	if !ok {
		t.Fatal("expected reminder")
	}

	// Should have warning for read_file only
	if !strings.Contains(content, "read_file") {
		t.Errorf("expected read_file in warning: %s", content)
	}
	// Should NOT have warning for write_file (only 1 error)
	if strings.Contains(content, "write_file") && strings.Contains(content, "⚠️ WARNING") {
		// Check if write_file is mentioned in WARNING context (not just tool history)
		lines := strings.Split(content, "\n")
		for _, line := range lines {
			if strings.Contains(line, "⚠️ WARNING") && strings.Contains(line, "write_file") {
				t.Errorf("expected no warning for write_file (only 1 error): %s", content)
			}
		}
	}
}

func TestConsecutiveSameTool_Warning(t *testing.T) {
	r := NewToolCallHistoryReminder()

	// 3 consecutive calls to the same tool
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "read_file")

	content, _, ok := r.GetContextReminder(context.Background(), "session-1")
	if !ok {
		t.Fatal("expected reminder")
	}
	if !strings.Contains(content, "You called \"read_file\" 3 times in a row") {
		t.Errorf("expected consecutive same-tool warning: %s", content)
	}
}

func TestConsecutiveSameTool_ResetOnDifferentTool(t *testing.T) {
	r := NewToolCallHistoryReminder()

	// 2 calls to read_file, then 1 to search_code
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "search_code")

	content, _, ok := r.GetContextReminder(context.Background(), "session-1")
	if !ok {
		t.Fatal("expected reminder")
	}
	// Should NOT have consecutive same-tool warning (count reset to 1 by search_code)
	if strings.Contains(content, "times in a row") {
		t.Errorf("expected no consecutive same-tool warning after different tool: %s", content)
	}
}

func TestConsecutiveSameTool_ResumesAfterReset(t *testing.T) {
	r := NewToolCallHistoryReminder()

	// read_file x2, search_code x1, read_file x3
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "search_code")
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "read_file")

	content, _, ok := r.GetContextReminder(context.Background(), "session-1")
	if !ok {
		t.Fatal("expected reminder")
	}
	// Should have warning for 3 consecutive read_file after the reset
	if !strings.Contains(content, "You called \"read_file\" 3 times in a row") {
		t.Errorf("expected consecutive same-tool warning: %s", content)
	}
}

func TestConsecutiveSameTool_BelowThreshold(t *testing.T) {
	r := NewToolCallHistoryReminder()

	// Only 2 consecutive calls (below threshold of 3)
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "search_code") // third call to trigger reminder

	content, _, ok := r.GetContextReminder(context.Background(), "session-1")
	if !ok {
		t.Fatal("expected reminder")
	}
	if strings.Contains(content, "times in a row") {
		t.Errorf("expected no warning for 2 consecutive calls: %s", content)
	}
}

func TestConsecutiveSameTool_SessionIsolation(t *testing.T) {
	r := NewToolCallHistoryReminder()

	// session-1: 3 consecutive read_file
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "read_file")

	// session-2: 3 different tools
	r.RecordToolCall("session-2", "read_file")
	r.RecordToolCall("session-2", "search_code")
	r.RecordToolCall("session-2", "write_file")

	// session-1 should have warning
	content1, _, ok := r.GetContextReminder(context.Background(), "session-1")
	if !ok {
		t.Fatal("expected reminder for session-1")
	}
	if !strings.Contains(content1, "times in a row") {
		t.Errorf("expected consecutive same-tool warning for session-1: %s", content1)
	}

	// session-2 should NOT have warning
	content2, _, ok := r.GetContextReminder(context.Background(), "session-2")
	if !ok {
		t.Fatal("expected reminder for session-2")
	}
	if strings.Contains(content2, "times in a row") {
		t.Errorf("expected no consecutive same-tool warning for session-2: %s", content2)
	}
}

func TestConsecutiveSameTool_ClearSession(t *testing.T) {
	r := NewToolCallHistoryReminder()

	// 3 consecutive calls
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "read_file")

	// Clear session
	r.ClearSession("session-1")

	// No reminder after clear
	_, _, ok := r.GetContextReminder(context.Background(), "session-1")
	if ok {
		t.Error("expected no reminder after clear")
	}

	// Start fresh: 1 call should not trigger warning
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "search_code")
	r.RecordToolCall("session-1", "write_file")

	content, _, ok := r.GetContextReminder(context.Background(), "session-1")
	if !ok {
		t.Fatal("expected reminder after re-adding calls")
	}
	if strings.Contains(content, "times in a row") {
		t.Errorf("expected no consecutive warning after clear and fresh calls: %s", content)
	}
}

func TestRecordToolResult_EmptyParams(t *testing.T) {
	r := NewToolCallHistoryReminder()

	// Should not crash on empty params
	r.RecordToolResult("", "read_file", "[ERROR] test")
	r.RecordToolResult("session-1", "", "[ERROR] test")
	r.RecordToolResult("session-1", "read_file", "")

	// Should have no side effects
	content, _, ok := r.GetContextReminder(context.Background(), "session-1")
	if ok {
		t.Errorf("expected no reminder after empty params: %s", content)
	}
}
