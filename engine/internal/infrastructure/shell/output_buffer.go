package shell

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const DefaultMaxSize = 1024 * 1024 // 1 MB

var markerRegex = regexp.MustCompile(`__SYNTHETICBREW_DONE_([a-f0-9]+)_(-?\d+)__`)

// MarkerResult holds the parsed result of a command execution marker.
type MarkerResult struct {
	ExitCode int
	Output   string
}

// OutputBuffer is a thread-safe ring buffer that accumulates process output
// and detects completion markers embedded in the stream.
type OutputBuffer struct {
	buffer  string
	maxSize int

	pendingID string
	pendingCh chan MarkerResult

	mu sync.Mutex
}

// NewOutputBuffer creates a new OutputBuffer with the given maximum size.
func NewOutputBuffer(maxSize int) *OutputBuffer {
	if maxSize <= 0 {
		maxSize = DefaultMaxSize
	}
	return &OutputBuffer{
		maxSize: maxSize,
	}
}

// Append adds a chunk of output to the buffer, trims if over maxSize,
// and checks for a pending marker match.
func (b *OutputBuffer) Append(chunk string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.buffer += chunk

	// Ring buffer trim
	if len(b.buffer) > b.maxSize {
		b.buffer = b.buffer[len(b.buffer)-b.maxSize:]
	}

	// Check for pending marker
	if b.pendingID == "" || b.pendingCh == nil {
		return
	}

	result, found := findMarker(b.buffer, b.pendingID)
	if !found {
		return
	}

	ch := b.pendingCh
	b.pendingID = ""
	b.pendingCh = nil

	// Send result non-blocking (channel is buffered)
	select {
	case ch <- result:
	default:
	}
}

// WaitForMarker blocks until the marker with the given ID appears in the buffer
// or the timeout expires. Returns the output before the marker and the exit code.
func (b *OutputBuffer) WaitForMarker(markerID string, timeout time.Duration) (MarkerResult, error) {
	b.mu.Lock()

	// Check if marker is already in the buffer
	if result, found := findMarker(b.buffer, markerID); found {
		b.mu.Unlock()
		return result, nil
	}

	ch := make(chan MarkerResult, 1)
	b.pendingID = markerID
	b.pendingCh = ch
	b.mu.Unlock()

	select {
	case result := <-ch:
		return result, nil
	case <-time.After(timeout):
		b.mu.Lock()
		b.pendingID = ""
		b.pendingCh = nil
		b.mu.Unlock()
		return MarkerResult{}, fmt.Errorf("timeout waiting for marker %s after %v", markerID, timeout)
	}
}

// GetOutput returns the current buffer content.
func (b *OutputBuffer) GetOutput() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer
}

// Reset clears the buffer.
func (b *OutputBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buffer = ""
}

// CancelPending cancels any pending marker wait without sending a result.
func (b *OutputBuffer) CancelPending() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.pendingCh != nil {
		close(b.pendingCh)
	}
	b.pendingID = ""
	b.pendingCh = nil
}

// WrapCommand wraps a command with a marker so that the output buffer can detect
// when the command finishes and what exit code it returned.
// Returns (markerID, wrappedCommand).
func WrapCommand(command string) (string, string) {
	markerID := randomHex(12)
	wrapped := fmt.Sprintf(
		"%s 2>&1; __vexit=$?; printf '\\n__SYNTHETICBREW_DONE_%s_%%d__\\n' $__vexit",
		command, markerID,
	)
	return markerID, wrapped
}

// findMarker searches the buffer for a specific marker ID and returns the output
// before the marker line along with the exit code.
func findMarker(buffer, markerID string) (MarkerResult, bool) {
	matches := markerRegex.FindAllStringSubmatchIndex(buffer, -1)
	for _, match := range matches {
		id := buffer[match[2]:match[3]]
		if id != markerID {
			continue
		}

		exitCodeStr := buffer[match[4]:match[5]]
		exitCode, err := strconv.Atoi(exitCodeStr)
		if err != nil {
			continue
		}

		// Output is everything before the marker line
		markerLineStart := match[0]
		output := buffer[:markerLineStart]
		output = strings.TrimRight(output, "\n")

		return MarkerResult{
			ExitCode: exitCode,
			Output:   output,
		}, true
	}
	return MarkerResult{}, false
}

// randomHex generates a random hex string of the given byte length.
func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:n]
}
