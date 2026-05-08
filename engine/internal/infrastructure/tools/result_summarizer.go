package tools

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Pre-compiled regex for performance (staticcheck SA6000)
var webSearchResultPattern = regexp.MustCompile(`^\d+\.`)

// SummarizeToolResult computes a short description of a tool result for the UI.
// Pure function: deterministic string parsing, no I/O, no LLM.
// Returns an empty string when no summary can be derived.
func SummarizeToolResult(toolName, result string) string {
	if result == "" {
		return ""
	}

	switch toolName {
	case "smart_search":
		return summarizeSmartSearch(result)
	case "manage_tasks":
		return firstLine(result)
	case "spawn_agent":
		return firstLine(result)
	case "lsp":
		return firstLine(result)
	default:
		return ""
	}
}

// summarizeSmartSearch parses the format "Found N results:\n\n1. path:line [source] ..."
func summarizeSmartSearch(result string) string {
	// Real format: "Found N results:\n\n1. file:10 [vector] ..."
	if strings.HasPrefix(result, "Found ") {
		parts := strings.Fields(result)
		if len(parts) >= 2 {
			if count, err := strconv.Atoi(parts[1]); err == nil {
				if count == 1 {
					return "1 citation"
				}
				return fmt.Sprintf("%d citations", count)
			}
		}
	}

	// Fallback: count numbered lines
	count := 0
	for _, line := range strings.SplitN(result, "\n", 200) {
		if webSearchResultPattern.MatchString(strings.TrimSpace(line)) {
			count++
		}
	}
	if count == 0 {
		return "0 citations"
	}
	if count == 1 {
		return "1 citation"
	}
	return fmt.Sprintf("%d citations", count)
}

// firstLine returns the first line, truncated to 60 runes (UTF-8 safe).
func firstLine(s string) string {
	lines := strings.SplitN(s, "\n", 2)
	if len(lines) == 0 {
		return ""
	}
	line := strings.TrimSpace(lines[0])
	runes := []rune(line)
	if len(runes) > 60 {
		return string(runes[:60]) + "..."
	}
	return line
}

// humanizeBytes converts bytes to a human-readable format (bytes/KB/MB)
func humanizeBytes(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d bytes", n)
	}
	kb := float64(n) / 1024.0
	if kb < 1024 {
		return fmt.Sprintf("%.1f KB", kb)
	}
	mb := kb / 1024.0
	return fmt.Sprintf("%.1f MB", mb)
}

// countOccurrences returns the number of occurrences of substr in s
func countOccurrences(s, substr string) int {
	return strings.Count(s, substr)
}
