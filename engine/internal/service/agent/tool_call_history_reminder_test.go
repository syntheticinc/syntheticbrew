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

// TestReminder_DistinctToolsNoWarning: three DIFFERENT tools form no loop, so the
// reminder emits nothing now that the routine per-step summary is gone.
func TestReminder_DistinctToolsNoWarning(t *testing.T) {
	r := NewToolCallHistoryReminder()
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "search_code")
	r.RecordToolCall("session-1", "smart_search")

	_, _, ok := r.GetContextReminder(context.Background(), "session-1")
	if ok {
		t.Error("expected no reminder: distinct tools, no loop, no error")
	}
}

// TestSameToolLoop_HasPriority98: a same-tool loop warning still uses priority 98.
func TestSameToolLoop_HasPriority98(t *testing.T) {
	r := NewToolCallHistoryReminder()
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "read_file")

	content, priority, ok := r.GetContextReminder(context.Background(), "session-1")
	if !ok {
		t.Fatal("expected same-tool loop warning")
	}
	if priority != 98 {
		t.Errorf("priority = %d, want 98", priority)
	}
	if !strings.Contains(content, "read_file") {
		t.Errorf("expected read_file named in warning: %s", content)
	}
	// Count-free: no climbing call number embedded.
	if strings.Contains(content, "(x3)") {
		t.Errorf("warning must not embed a per-step count: %s", content)
	}
}

// TestNoWarning_WhenLoopBrokenByDifferentTool: read_file x2 then smart_search forms
// no same-tool loop, so no warning is emitted.
func TestNoWarning_WhenLoopBrokenByDifferentTool(t *testing.T) {
	r := NewToolCallHistoryReminder()
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "smart_search")

	_, _, ok := r.GetContextReminder(context.Background(), "session-1")
	if ok {
		t.Error("expected no reminder: same-tool streak broken, no loop, no error")
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

// TestErrorLoops_SortedDeterministicOrder: when multiple tools are in error loops,
// the warnings appear in sorted tool order so the emitted text is byte-identical
// across calls (required for the trailing-nudge dedup to keep it append-once).
func TestErrorLoops_SortedDeterministicOrder(t *testing.T) {
	r := NewToolCallHistoryReminder()
	r.RecordToolCall("session-1", "write_file")
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "edit_file")

	for _, tool := range []string{"write_file", "read_file", "edit_file"} {
		for i := 0; i < loopThreshold; i++ {
			r.RecordToolResult("session-1", tool, "[ERROR] boom")
		}
	}

	content, _, ok := r.GetContextReminder(context.Background(), "session-1")
	if !ok {
		t.Fatal("expected error-loop warnings")
	}

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

	// After 100 concurrent same-tool calls, a count-free same-tool loop warning fires.
	content, _, ok := r.GetContextReminder(context.Background(), "session-1")
	if !ok {
		t.Fatal("expected same-tool loop warning after concurrent writes")
	}
	if !strings.Contains(content, "read_file") {
		t.Errorf("expected read_file named in warning: %s", content)
	}
	if strings.ContainsAny(content, "0123456789") {
		t.Errorf("warning must be count-free (no digits): %s", content)
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
	if !strings.Contains(content, "calling \"read_file\" repeatedly") {
		t.Errorf("expected count-free consecutive same-tool warning: %s", content)
	}
}

func TestConsecutiveSameTool_ResetOnDifferentTool(t *testing.T) {
	r := NewToolCallHistoryReminder()

	// 2 calls to read_file, then 1 to search_code → streak broken, no loop, no warning.
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "search_code")

	_, _, ok := r.GetContextReminder(context.Background(), "session-1")
	if ok {
		t.Error("expected no reminder after same-tool streak broken by a different tool")
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
	if !strings.Contains(content, "calling \"read_file\" repeatedly") {
		t.Errorf("expected count-free consecutive same-tool warning: %s", content)
	}
}

func TestConsecutiveSameTool_BelowThreshold(t *testing.T) {
	r := NewToolCallHistoryReminder()

	// Only 2 consecutive read_file then a different tool — below the loop threshold,
	// no error loop either → no warning.
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "search_code")

	_, _, ok := r.GetContextReminder(context.Background(), "session-1")
	if ok {
		t.Error("expected no reminder for 2 consecutive calls (below loop threshold)")
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

	// session-1 should have the same-tool loop warning
	content1, _, ok := r.GetContextReminder(context.Background(), "session-1")
	if !ok {
		t.Fatal("expected reminder for session-1")
	}
	if !strings.Contains(content1, "calling \"read_file\" repeatedly") {
		t.Errorf("expected consecutive same-tool warning for session-1: %s", content1)
	}

	// session-2 has 3 distinct tools — no loop, no error → no reminder at all.
	_, _, ok = r.GetContextReminder(context.Background(), "session-2")
	if ok {
		t.Error("expected no reminder for session-2 (distinct tools, no loop)")
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

	// Start fresh: 3 distinct tools must not re-trigger the prior same-tool loop.
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "search_code")
	r.RecordToolCall("session-1", "write_file")

	_, _, ok = r.GetContextReminder(context.Background(), "session-1")
	if ok {
		t.Error("expected no consecutive warning after clear and fresh distinct calls")
	}
}

// TestNoReminder_WhenNoWarning guards the cache-stability redesign: with tool calls
// but NO loop/error condition, GetContextReminder must emit nothing (ok=false). The
// routine per-step "you called X(x3)" summary was redundant with the transcript AND
// changed every step, so it was removed — only loop/error warnings remain.
func TestNoReminder_WhenNoWarning(t *testing.T) {
	r := NewToolCallHistoryReminder()
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "search_code")
	r.RecordToolCall("session-1", "smart_search")

	_, _, ok := r.GetContextReminder(context.Background(), "session-1")
	if ok {
		t.Error("expected no reminder when there is no loop/error warning to give")
	}
}

// TestSameToolWarning_IsCountFreeAndStable guards that the same-tool loop warning
// carries no changing count number and is byte-identical call-to-call, so the
// trailing-nudge dedup treats it as a single append-once item (cache-stable).
func TestSameToolWarning_IsCountFreeAndStable(t *testing.T) {
	r := NewToolCallHistoryReminder()
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "read_file")

	first, _, ok := r.GetContextReminder(context.Background(), "session-1")
	if !ok {
		t.Fatal("expected same-tool loop warning")
	}
	if strings.ContainsAny(first, "0123456789") {
		t.Errorf("warning must be count-free (no digits): %s", first)
	}

	// One more identical call keeps the count climbing internally, but the emitted
	// text must not change — otherwise the dedup re-appends it and breaks append-only.
	r.RecordToolCall("session-1", "read_file")
	second, _, ok := r.GetContextReminder(context.Background(), "session-1")
	if !ok {
		t.Fatal("expected same-tool loop warning on the next call too")
	}
	if first != second {
		t.Errorf("same-tool warning text must be stable across calls: %q vs %q", first, second)
	}
}

// TestErrorLoopWarning_IsCountFree guards the error-loop warning is count-free too.
func TestErrorLoopWarning_IsCountFree(t *testing.T) {
	r := NewToolCallHistoryReminder()
	// Three different tools so the same-tool loop warning does NOT fire — isolate the
	// error-loop warning text.
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolCall("session-1", "search_code")
	r.RecordToolCall("session-1", "read_file")
	r.RecordToolResult("session-1", "read_file", "[ERROR] not found")
	r.RecordToolResult("session-1", "read_file", "[ERROR] not found")
	r.RecordToolResult("session-1", "read_file", "[ERROR] not found")

	content, _, ok := r.GetContextReminder(context.Background(), "session-1")
	if !ok {
		t.Fatal("expected error-loop warning")
	}
	if strings.ContainsAny(content, "0123456789") {
		t.Errorf("error-loop warning must be count-free (no digits): %s", content)
	}
	if !strings.Contains(content, "read_file") {
		t.Errorf("error-loop warning must still name the offending tool: %s", content)
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
