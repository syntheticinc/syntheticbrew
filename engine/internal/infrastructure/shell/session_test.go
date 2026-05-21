package shell

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func skipIfNoShell(t *testing.T) {
	t.Helper()
	shell := detectShell()
	if shell == "bash" && runtime.GOOS == "windows" {
		// On Windows, "bash" without a full path means it wasn't found
		t.Skip("bash not found in PATH (Git Bash required on Windows)")
	}
}

func TestShellSession_SimpleCommand(t *testing.T) {
	skipIfNoShell(t)

	session := NewShellSession(t.TempDir(), DefaultMaxSize)
	defer session.Destroy()

	result, err := session.Execute(context.Background(), "echo hello", 10*time.Second)
	require.NoError(t, err)
	require.True(t, result.Completed)
	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, "hello", strings.TrimSpace(result.Stdout))
}

func TestShellSession_PersistentState(t *testing.T) {
	skipIfNoShell(t)

	session := NewShellSession(t.TempDir(), DefaultMaxSize)
	defer session.Destroy()

	ctx := context.Background()

	// Export a variable
	result, err := session.Execute(ctx, "export TESTVAR=syntheticbrew42", 10*time.Second)
	require.NoError(t, err)
	require.True(t, result.Completed)

	// Read it back — should persist across Execute calls
	result, err = session.Execute(ctx, "echo $TESTVAR", 10*time.Second)
	require.NoError(t, err)
	require.True(t, result.Completed)
	assert.Equal(t, "syntheticbrew42", strings.TrimSpace(result.Stdout))
}

func TestShellSession_ExitCode(t *testing.T) {
	skipIfNoShell(t)

	session := NewShellSession(t.TempDir(), DefaultMaxSize)
	defer session.Destroy()

	// Use a subshell so the exit code is captured without killing the persistent shell
	result, err := session.Execute(context.Background(), "(exit 42)", 10*time.Second)
	require.NoError(t, err)
	require.True(t, result.Completed)
	assert.Equal(t, 42, result.ExitCode)
}

func TestShellSession_MultilineOutput(t *testing.T) {
	skipIfNoShell(t)

	session := NewShellSession(t.TempDir(), DefaultMaxSize)
	defer session.Destroy()

	result, err := session.Execute(context.Background(), "echo line1; echo line2; echo line3", 10*time.Second)
	require.NoError(t, err)
	require.True(t, result.Completed)

	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	assert.Len(t, lines, 3)
	assert.Equal(t, "line1", strings.TrimSpace(lines[0]))
	assert.Equal(t, "line2", strings.TrimSpace(lines[1]))
	assert.Equal(t, "line3", strings.TrimSpace(lines[2]))
}

func TestShellSession_IsExecuting(t *testing.T) {
	skipIfNoShell(t)

	session := NewShellSession(t.TempDir(), DefaultMaxSize)
	defer session.Destroy()

	assert.False(t, session.IsExecuting())
}

func TestShellSession_Timeout(t *testing.T) {
	skipIfNoShell(t)

	session := NewShellSession(t.TempDir(), DefaultMaxSize)
	defer session.Destroy()

	result, err := session.Execute(context.Background(), "sleep 30", 500*time.Millisecond)
	require.NoError(t, err)
	assert.False(t, result.Completed)
	assert.Equal(t, -1, result.ExitCode)
}

func TestSessionManager_PoolSize(t *testing.T) {
	skipIfNoShell(t)

	mgr := NewSessionManager()
	defer mgr.DisposeAll()

	root := t.TempDir()
	var sessions []*ShellSession

	// Get PoolSize sessions, marking each as executing before getting the next
	for i := 0; i < PoolSize; i++ {
		s := mgr.GetAvailableSession(root, "agent1")
		require.NotNil(t, s, "session %d should not be nil", i)
		s.isExecuting.Store(true)
		sessions = append(sessions, s)
	}

	// Should return nil when all busy
	s := mgr.GetAvailableSession(root, "agent1")
	assert.Nil(t, s, "should return nil when all sessions are busy")

	// Free one
	sessions[0].isExecuting.Store(false)
	s = mgr.GetAvailableSession(root, "agent1")
	assert.NotNil(t, s, "should return freed session")
}

func TestBackgroundProcess_SpawnAndRead(t *testing.T) {
	skipIfNoShell(t)

	bgm := NewBackgroundProcessManager()
	defer bgm.DisposeAll()

	proc, err := bgm.Spawn("echo background_output", t.TempDir())
	require.NoError(t, err)
	assert.NotEmpty(t, proc.ID)
	assert.Greater(t, proc.PID, 0)

	// Wait for process to finish
	time.Sleep(500 * time.Millisecond)

	output, err := bgm.ReadOutput(proc.ID)
	require.NoError(t, err)
	assert.Contains(t, output, "background_output")
}

func TestBackgroundProcess_List(t *testing.T) {
	skipIfNoShell(t)

	bgm := NewBackgroundProcessManager()
	defer bgm.DisposeAll()

	assert.Empty(t, bgm.List())

	_, err := bgm.Spawn("echo test1", t.TempDir())
	require.NoError(t, err)

	_, err = bgm.Spawn("echo test2", t.TempDir())
	require.NoError(t, err)

	// Wait for fast processes to exit so DisposeAll doesn't try to kill dead PIDs
	time.Sleep(500 * time.Millisecond)

	procs := bgm.List()
	assert.Len(t, procs, 2)
}

func TestBackgroundProcess_Kill(t *testing.T) {
	skipIfNoShell(t)

	bgm := NewBackgroundProcessManager()
	defer bgm.DisposeAll()

	proc, err := bgm.Spawn("sleep 300", t.TempDir())
	require.NoError(t, err)

	err = bgm.Kill(proc.ID)
	require.NoError(t, err)

	// Wait for waitForExit goroutine to update status
	// On Windows, taskkill + process cleanup can take a few seconds
	require.Eventually(t, func() bool {
		procs := bgm.List()
		for _, p := range procs {
			if p.ID == proc.ID {
				return p.Status == "exited"
			}
		}
		return false
	}, 10*time.Second, 200*time.Millisecond, "process should have exited after kill")
}

func TestBackgroundProcess_ReadNotFound(t *testing.T) {
	bgm := NewBackgroundProcessManager()
	_, err := bgm.ReadOutput("nonexistent")
	assert.Error(t, err)
}
