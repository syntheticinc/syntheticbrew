package flowregistry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

func TestRegister_Success(t *testing.T) {
	registry := NewInMemoryRegistry()

	flow, err := domain.NewActiveFlow("session-1", "project-1", "user-1", "test task")
	require.NoError(t, err)

	err = registry.Register(flow.SessionID, flow, nil)
	require.NoError(t, err)

	assert.True(t, registry.IsActive(flow.SessionID))
}

func TestRegister_ReplacesExisting(t *testing.T) {
	registry := NewInMemoryRegistry()

	ctx1, cancel1 := context.WithCancel(context.Background())

	flow1, err := domain.NewActiveFlow("session-1", "project-1", "user-1", "task 1")
	require.NoError(t, err)

	err = registry.Register(flow1.SessionID, flow1, cancel1)
	require.NoError(t, err)

	flow2, err := domain.NewActiveFlow("session-1", "project-1", "user-1", "task 2")
	require.NoError(t, err)

	err = registry.Register(flow2.SessionID, flow2, nil)
	require.NoError(t, err)

	// Old flow's context should be cancelled
	assert.Error(t, ctx1.Err(), "expected old flow's context to be cancelled")

	retrieved, found := registry.Get("session-1")
	require.True(t, found)
	assert.Equal(t, flow2, retrieved, "expected registry to contain the new flow")
}

func TestRegister_ReplacesExisting_NilCancel(t *testing.T) {
	registry := NewInMemoryRegistry()

	flow1, err := domain.NewActiveFlow("session-1", "project-1", "user-1", "task 1")
	require.NoError(t, err)

	// Register with nil cancel -- should not panic on replacement
	err = registry.Register(flow1.SessionID, flow1, nil)
	require.NoError(t, err)

	flow2, err := domain.NewActiveFlow("session-1", "project-1", "user-1", "task 2")
	require.NoError(t, err)

	err = registry.Register(flow2.SessionID, flow2, nil)
	require.NoError(t, err)

	retrieved, found := registry.Get("session-1")
	require.True(t, found)
	assert.Equal(t, flow2, retrieved)
}

func TestUnregister_Success(t *testing.T) {
	registry := NewInMemoryRegistry()

	flow, err := domain.NewActiveFlow("session-1", "project-1", "user-1", "test task")
	require.NoError(t, err)

	err = registry.Register(flow.SessionID, flow, nil)
	require.NoError(t, err)

	err = registry.Unregister(flow.SessionID)
	require.NoError(t, err)

	assert.False(t, registry.IsActive(flow.SessionID))
}

func TestUnregister_Idempotent(t *testing.T) {
	registry := NewInMemoryRegistry()

	// Unregister non-existent session -- no error
	err := registry.Unregister("nonexistent")
	assert.NoError(t, err)

	// Register and unregister twice
	flow, err := domain.NewActiveFlow("s1", "p1", "u1", "t1")
	require.NoError(t, err)

	err = registry.Register("s1", flow, nil)
	require.NoError(t, err)

	err = registry.Unregister("s1")
	assert.NoError(t, err)

	err = registry.Unregister("s1") // second call
	assert.NoError(t, err)
}

func TestUnregisterIfCurrent(t *testing.T) {
	registry := NewInMemoryRegistry()

	flow1, err := domain.NewActiveFlow("s1", "p1", "u1", "t1")
	require.NoError(t, err)

	flow2, err := domain.NewActiveFlow("s1", "p1", "u1", "t2")
	require.NoError(t, err)

	err = registry.Register("s1", flow1, nil)
	require.NoError(t, err)

	// Replace with flow2
	err = registry.Register("s1", flow2, nil)
	require.NoError(t, err)

	// UnregisterIfCurrent with flow1 should NOT unregister (replaced)
	removed := registry.UnregisterIfCurrent("s1", flow1)
	assert.False(t, removed, "stale flow should not be unregistered")

	// flow2 should still be there
	current, exists := registry.Get("s1")
	assert.True(t, exists)
	assert.Equal(t, flow2, current)

	// UnregisterIfCurrent with flow2 should succeed
	removed = registry.UnregisterIfCurrent("s1", flow2)
	assert.True(t, removed, "current flow should be unregistered")

	_, exists = registry.Get("s1")
	assert.False(t, exists)
}

func TestUnregisterIfCurrent_NonExistent(t *testing.T) {
	registry := NewInMemoryRegistry()

	flow, err := domain.NewActiveFlow("s1", "p1", "u1", "t1")
	require.NoError(t, err)

	removed := registry.UnregisterIfCurrent("nonexistent", flow)
	assert.False(t, removed)
}

func TestGet_Found(t *testing.T) {
	registry := NewInMemoryRegistry()

	flow, err := domain.NewActiveFlow("session-1", "project-1", "user-1", "test task")
	require.NoError(t, err)

	err = registry.Register(flow.SessionID, flow, nil)
	require.NoError(t, err)

	retrieved, found := registry.Get(flow.SessionID)
	require.True(t, found)
	assert.Equal(t, flow.SessionID, retrieved.SessionID)
}

func TestGet_NotFound(t *testing.T) {
	registry := NewInMemoryRegistry()

	_, found := registry.Get("non-existent")
	assert.False(t, found)
}

func TestIsActive_True(t *testing.T) {
	registry := NewInMemoryRegistry()

	flow, err := domain.NewActiveFlow("session-1", "project-1", "user-1", "test task")
	require.NoError(t, err)

	err = registry.Register(flow.SessionID, flow, nil)
	require.NoError(t, err)

	assert.True(t, registry.IsActive(flow.SessionID))
}

func TestIsActive_False(t *testing.T) {
	registry := NewInMemoryRegistry()

	assert.False(t, registry.IsActive("non-existent"))
}

func TestConcurrentAccess(t *testing.T) {
	registry := NewInMemoryRegistry()

	done := make(chan bool)

	// Concurrent registrations
	for i := 0; i < 10; i++ {
		go func(id int) {
			flow, err := domain.NewActiveFlow(
				"session-"+string(rune('0'+id)),
				"project-1",
				"user-1",
				"test task",
			)
			if err != nil {
				t.Errorf("failed to create flow: %v", err)
				done <- false
				return
			}

			registry.Register(flow.SessionID, flow, nil)
			registry.IsActive(flow.SessionID)
			registry.Get(flow.SessionID)
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		if !<-done {
			t.Error("concurrent operation failed")
		}
	}
}
