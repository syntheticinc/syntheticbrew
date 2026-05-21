package shell

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWrapCommand_Format(t *testing.T) {
	markerID, wrapped := WrapCommand("echo hello")

	assert.Len(t, markerID, 12, "markerID should be 12 hex chars")
	assert.Contains(t, wrapped, "echo hello")
	assert.Contains(t, wrapped, "2>&1")
	assert.Contains(t, wrapped, "__SYNTHETICBREW_DONE_"+markerID)
	assert.Contains(t, wrapped, "__vexit=$?")
}

func TestOutputBuffer_MarkerDetection(t *testing.T) {
	buf := NewOutputBuffer(DefaultMaxSize)

	markerID, _ := WrapCommand("echo hello")

	// Simulate process output
	buf.Append("hello\n")
	buf.Append(fmt.Sprintf("__SYNTHETICBREW_DONE_%s_0__\n", markerID))

	result, found := findMarker(buf.GetOutput(), markerID)
	require.True(t, found)
	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, "hello", result.Output)
}

func TestOutputBuffer_MarkerDetection_NonZeroExit(t *testing.T) {
	buf := NewOutputBuffer(DefaultMaxSize)

	markerID := "abcdef012345"
	buf.Append("some error output\n")
	buf.Append(fmt.Sprintf("__SYNTHETICBREW_DONE_%s_42__\n", markerID))

	result, found := findMarker(buf.GetOutput(), markerID)
	require.True(t, found)
	assert.Equal(t, 42, result.ExitCode)
	assert.Equal(t, "some error output", result.Output)
}

func TestOutputBuffer_MarkerDetection_NegativeExit(t *testing.T) {
	buf := NewOutputBuffer(DefaultMaxSize)

	markerID := "abcdef012345"
	buf.Append("output\n")
	buf.Append(fmt.Sprintf("__SYNTHETICBREW_DONE_%s_-1__\n", markerID))

	result, found := findMarker(buf.GetOutput(), markerID)
	require.True(t, found)
	assert.Equal(t, -1, result.ExitCode)
}

func TestOutputBuffer_WaitForMarker_AlreadyPresent(t *testing.T) {
	buf := NewOutputBuffer(DefaultMaxSize)

	markerID := "aabbccddeeff"
	buf.Append("done\n")
	buf.Append(fmt.Sprintf("__SYNTHETICBREW_DONE_%s_0__\n", markerID))

	result, err := buf.WaitForMarker(markerID, time.Second)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, "done", result.Output)
}

func TestOutputBuffer_WaitForMarker_ArrivesLater(t *testing.T) {
	buf := NewOutputBuffer(DefaultMaxSize)
	markerID := "aabbccddeeff"

	go func() {
		time.Sleep(50 * time.Millisecond)
		buf.Append("output line\n")
		buf.Append(fmt.Sprintf("__SYNTHETICBREW_DONE_%s_0__\n", markerID))
	}()

	result, err := buf.WaitForMarker(markerID, 2*time.Second)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, "output line", result.Output)
}

func TestOutputBuffer_WaitForMarker_Timeout(t *testing.T) {
	buf := NewOutputBuffer(DefaultMaxSize)

	_, err := buf.WaitForMarker("nonexistent123", 100*time.Millisecond)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timeout")
}

func TestOutputBuffer_RingBuffer_Trim(t *testing.T) {
	maxSize := 100
	buf := NewOutputBuffer(maxSize)

	// Write more than maxSize
	longData := strings.Repeat("x", 200)
	buf.Append(longData)

	output := buf.GetOutput()
	assert.Equal(t, maxSize, len(output))
	// Should keep the tail
	assert.Equal(t, strings.Repeat("x", maxSize), output)
}

func TestOutputBuffer_Reset(t *testing.T) {
	buf := NewOutputBuffer(DefaultMaxSize)
	buf.Append("data")
	assert.NotEmpty(t, buf.GetOutput())

	buf.Reset()
	assert.Empty(t, buf.GetOutput())
}

func TestOutputBuffer_MultilineOutput(t *testing.T) {
	buf := NewOutputBuffer(DefaultMaxSize)
	markerID := "aabbccddeeff"

	buf.Append("line1\n")
	buf.Append("line2\n")
	buf.Append("line3\n")
	buf.Append(fmt.Sprintf("__SYNTHETICBREW_DONE_%s_0__\n", markerID))

	result, found := findMarker(buf.GetOutput(), markerID)
	require.True(t, found)
	assert.Equal(t, "line1\nline2\nline3", result.Output)
}

func TestOutputBuffer_WrongMarkerID(t *testing.T) {
	buf := NewOutputBuffer(DefaultMaxSize)

	buf.Append("output\n")
	buf.Append("__SYNTHETICBREW_DONE_aabbccddeeff_0__\n")

	_, found := findMarker(buf.GetOutput(), "112233445566")
	assert.False(t, found)
}
