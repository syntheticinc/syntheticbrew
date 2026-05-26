package agents

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureSlog redirects the default slog logger to a bytes.Buffer for
// the duration of the test, returning the buffer + a cleanup func.
// We capture INFO and above so we can distinguish the new graceful
// "context logging disabled" INFO line from the prior noisy ERROR.
func captureSlog(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()
	buf := &bytes.Buffer{}
	handler := slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	prev := slog.Default()
	slog.SetDefault(slog.New(handler))
	return buf, func() { slog.SetDefault(prev) }
}

// TestContextLogger_StickyDisableOnMkdirFailure verifies the WARN B fix:
// when MkdirAll on the session directory fails, the very first call
// logs exactly one INFO line announcing the disable; all subsequent
// calls to LogContextSummary / LogContext / LogCompressionReport /
// LogSessionOverview return early silently.
//
// Prior behaviour was an ERROR per turn — partner logs were flooded.
//
// To portably force MkdirAll failure (without relying on POSIX chmod),
// we pre-create a FILE at the exact path the logger will try to mkdir;
// MkdirAll returns "not a directory" on every OS in that scenario.
func TestContextLogger_StickyDisableOnMkdirFailure(t *testing.T) {
	root := t.TempDir()

	// Force a deterministic session directory name so we can occupy it
	// with a file before the logger tries to MkdirAll it.
	sessionID := "00000000-0000-0000-0000-000000000abc"
	cl := NewContextLogger(root, sessionID)
	// Place a regular file at the path the logger will try to create
	// as a directory; MkdirAll returns an error in this scenario on
	// every OS.
	conflict := filepath.Join(root, cl.GetSessionDirName())
	require.NoError(t, os.WriteFile(conflict, []byte("not a directory"), 0o600))

	buf, restore := captureSlog(t)
	defer restore()
	ctx := context.Background()

	// First call should attempt mkdir, fail, log INFO once, set disabled.
	cl.LogContextSummary(ctx, []*schema.Message{{Role: schema.User, Content: "hi"}})

	// Subsequent calls — via every public log method — must NOT emit
	// further log lines (they short-circuit on the sticky flag).
	cl.LogContextSummary(ctx, nil)
	cl.LogContext(ctx, nil, 0)
	cl.LogCompressionReport(ctx, 10, 5, nil)
	cl.LogSessionOverview(SessionOverview{SessionID: "abc"})

	out := buf.String()
	count := strings.Count(out, "context logging disabled")
	assert.Equal(t, 1, count,
		"expected exactly ONE 'context logging disabled' INFO line, got %d.\nFull output:\n%s",
		count, out)
	assert.NotContains(t, out, "failed to create session log directory",
		"old ERROR phrasing must no longer appear after sticky-disable")
}

// TestContextLogger_EnsureDirSucceedsOnWritablePath verifies the happy
// path: a writable directory results in mkdir success and no INFO-level
// disable log line.
func TestContextLogger_EnsureDirSucceedsOnWritablePath(t *testing.T) {
	root := t.TempDir()
	buf, restore := captureSlog(t)
	defer restore()

	cl := NewContextLogger(root, "00000000-0000-0000-0000-000000000def")
	cl.LogContextSummary(context.Background(), []*schema.Message{{Role: schema.User, Content: "ok"}})

	assert.NotContains(t, buf.String(), "context logging disabled",
		"writable FS must not trigger the disable INFO log")
}
