package flowregistry

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	pb "github.com/syntheticinc/syntheticbrew/api/proto/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	"github.com/syntheticinc/syntheticbrew/internal/service/eventstore"
	"gorm.io/gorm"
)

// newTestEventStore creates an in-memory event store for tests.
func newTestEventStore(t *testing.T) *eventstore.Store {
	t.Helper()
	gormDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gormDB.AutoMigrate(&models.SessionEventLogModel{}))
	store, err := eventstore.New(gormDB)
	require.NoError(t, err)
	return store
}

// TC-G-01: CreateSession — create session, verify session_id returned and context accessible
func TestSessionRegistry_CreateAndGetContext(t *testing.T) {
	reg := NewSessionRegistry(nil)
	reg.CreateSession("s1", "proj-1", "user-1", "/root", "linux", "")

	root, platform, key, user, agentName, ok := reg.GetSessionContext("s1")
	require.True(t, ok)
	assert.Equal(t, "/root", root)
	assert.Equal(t, "linux", platform)
	assert.Equal(t, "proj-1", key)
	assert.Equal(t, "user-1", user)
	assert.Empty(t, agentName)
}

func TestSessionRegistry_GetContext_NotFound(t *testing.T) {
	reg := NewSessionRegistry(nil)

	_, _, _, _, _, ok := reg.GetSessionContext("nonexistent")
	assert.False(t, ok)
}

func TestSessionRegistry_HasSession(t *testing.T) {
	reg := NewSessionRegistry(nil)

	assert.False(t, reg.HasSession("s1"))

	reg.CreateSession("s1", "proj", "user", "/root", "linux", "")
	assert.True(t, reg.HasSession("s1"))
}

func TestSessionRegistry_SubscribeAndPublish(t *testing.T) {
	reg := NewSessionRegistry(nil)
	reg.CreateSession("s1", "proj", "user", "/root", "linux", "")

	ch, cleanup := reg.Subscribe("s1")
	defer cleanup()

	event := &pb.SessionEvent{
		EventId:   "evt-1",
		SessionId: "s1",
		Type:      pb.SessionEventType_SESSION_EVENT_ANSWER,
		Content:   "hello",
	}

	reg.PublishEvent("s1", event)

	select {
	case received := <-ch:
		assert.Equal(t, "evt-1", received.EventId)
		assert.Equal(t, "hello", received.Content)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestSessionRegistry_MultipleSubscribers(t *testing.T) {
	reg := NewSessionRegistry(nil)
	reg.CreateSession("s1", "proj", "user", "/root", "linux", "")

	ch1, cleanup1 := reg.Subscribe("s1")
	defer cleanup1()

	ch2, cleanup2 := reg.Subscribe("s1")
	defer cleanup2()

	event := &pb.SessionEvent{EventId: "evt-1", Content: "broadcast"}
	reg.PublishEvent("s1", event)

	select {
	case received := <-ch1:
		assert.Equal(t, "broadcast", received.Content)
	case <-time.After(time.Second):
		t.Fatal("subscriber 1 timed out")
	}

	select {
	case received := <-ch2:
		assert.Equal(t, "broadcast", received.Content)
	case <-time.After(time.Second):
		t.Fatal("subscriber 2 timed out")
	}
}

func TestSessionRegistry_SubscribeNonExistent(t *testing.T) {
	reg := NewSessionRegistry(nil)

	ch, cleanup := reg.Subscribe("nonexistent")
	defer cleanup()

	// Channel should be closed immediately
	_, ok := <-ch
	assert.False(t, ok)
}

func TestSessionRegistry_ReplayEvents(t *testing.T) {
	store := newTestEventStore(t)
	reg := NewSessionRegistry(store)
	reg.CreateSession("s1", "proj", "user", "/root", "linux", "")

	// Append 3 events via store (simulating what EventStream does)
	var ids [3]string
	for i := 0; i < 3; i++ {
		id, err := store.Append("s1", "answer", &pb.SessionEvent{
			Content: fmt.Sprintf("msg-%d", i+1),
			Type:    pb.SessionEventType_SESSION_EVENT_ANSWER,
		}, map[string]interface{}{"type": "MessageCompleted", "content": fmt.Sprintf("msg-%d", i+1)})
		require.NoError(t, err)
		ids[i] = id
	}

	// Replay after first event should return 2nd and 3rd
	replayed := reg.ReplayEvents("s1", ids[0])
	require.Len(t, replayed, 2)
	assert.Equal(t, "msg-2", replayed[0].Content)
	assert.Equal(t, "msg-3", replayed[1].Content)

	// Replay after last event should return nothing
	replayed = reg.ReplayEvents("s1", ids[2])
	assert.Empty(t, replayed)

	// Replay with empty string should return full history
	replayed = reg.ReplayEvents("s1", "")
	require.Len(t, replayed, 3)
	assert.Equal(t, "msg-1", replayed[0].Content)
	assert.Equal(t, "msg-2", replayed[1].Content)
	assert.Equal(t, "msg-3", replayed[2].Content)
}

func TestSessionRegistry_EnqueueDequeueMessage(t *testing.T) {
	reg := NewSessionRegistry(nil)
	reg.CreateSession("s1", "proj", "user", "/root", "linux", "")

	err := reg.EnqueueMessage("s1", "hello")
	require.NoError(t, err)

	msg, ok := reg.DequeueMessage("s1")
	assert.True(t, ok)
	assert.Equal(t, "hello", msg)
}

func TestSessionRegistry_EnqueueMessage_NotFound(t *testing.T) {
	reg := NewSessionRegistry(nil)

	err := reg.EnqueueMessage("nonexistent", "hello")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")
}

func TestSessionRegistry_AskUserReply(t *testing.T) {
	reg := NewSessionRegistry(nil)
	reg.CreateSession("s1", "proj", "user", "/root", "linux", "")

	replyCh := reg.RegisterAskUser("s1", "call-1")

	go func() {
		time.Sleep(10 * time.Millisecond)
		reg.SendAskUserReply("s1", "call-1", "yes")
	}()

	select {
	case reply := <-replyCh:
		assert.Equal(t, "yes", reply)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reply")
	}

	reg.UnregisterAskUser("s1", "call-1")
}

// TC-G-04: Reasoning events — publish reasoning event, verify subscriber receives it
func TestSessionRegistry_ReasoningEvent(t *testing.T) {
	reg := NewSessionRegistry(nil)
	reg.CreateSession("s1", "proj", "user", "/root", "linux", "")

	ch, cleanup := reg.Subscribe("s1")
	defer cleanup()

	event := &pb.SessionEvent{
		EventId:   "evt-reasoning-1",
		SessionId: "s1",
		Type:      pb.SessionEventType_SESSION_EVENT_REASONING,
		Content:   "I need to analyze the project structure first...",
	}

	reg.PublishEvent("s1", event)

	select {
	case received := <-ch:
		assert.Equal(t, "evt-reasoning-1", received.EventId)
		assert.Equal(t, pb.SessionEventType_SESSION_EVENT_REASONING, received.Type)
		assert.Equal(t, "I need to analyze the project structure first...", received.Content)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reasoning event")
	}
}

// TC-G-05: Plan update events — publish plan event, verify subscriber receives it
func TestSessionRegistry_PlanUpdateEvent(t *testing.T) {
	reg := NewSessionRegistry(nil)
	reg.CreateSession("s1", "proj", "user", "/root", "linux", "")

	ch, cleanup := reg.Subscribe("s1")
	defer cleanup()

	event := &pb.SessionEvent{
		EventId:   "evt-plan-1",
		SessionId: "s1",
		Type:      pb.SessionEventType_SESSION_EVENT_PLAN_UPDATE,
		Content:   `{"title":"Analyze project","steps":[{"description":"Read config","status":"done"}]}`,
	}

	reg.PublishEvent("s1", event)

	select {
	case received := <-ch:
		assert.Equal(t, "evt-plan-1", received.EventId)
		assert.Equal(t, pb.SessionEventType_SESSION_EVENT_PLAN_UPDATE, received.Type)
		assert.Contains(t, received.Content, "Analyze project")
		assert.Contains(t, received.Content, "Read config")
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for plan update event")
	}
}

// TC-G-07: CancelTask — create session, cancel, verify cancellation
func TestSessionRegistry_Cancel(t *testing.T) {
	reg := NewSessionRegistry(nil)
	reg.CreateSession("s1", "proj", "user", "/root", "linux", "")

	assert.False(t, reg.IsCancelled("s1"))

	cancelled := reg.Cancel("s1")
	assert.True(t, cancelled)
	assert.True(t, reg.IsCancelled("s1"))
}

func TestSessionRegistry_Cancel_NotFound(t *testing.T) {
	reg := NewSessionRegistry(nil)

	cancelled := reg.Cancel("nonexistent")
	assert.False(t, cancelled)
}

func TestSessionRegistry_RemoveSession(t *testing.T) {
	reg := NewSessionRegistry(nil)
	reg.CreateSession("s1", "proj", "user", "/root", "linux", "")

	ch, cleanup := reg.Subscribe("s1")
	defer cleanup()

	reg.RemoveSession("s1")

	assert.False(t, reg.HasSession("s1"))

	// Subscriber channel should be closed
	_, ok := <-ch
	assert.False(t, ok)
}

func TestSessionRegistry_ConcurrentAccess(t *testing.T) {
	reg := NewSessionRegistry(nil)
	reg.CreateSession("s1", "proj", "user", "/root", "linux", "")

	var wg sync.WaitGroup

	// Concurrent publishers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			reg.PublishEvent("s1", &pb.SessionEvent{
				EventId: fmt.Sprintf("evt-%d", id),
				Content: "hello",
			})
		}(i)
	}

	// Concurrent subscribers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch, cleanup := reg.Subscribe("s1")
			defer cleanup()
			// Drain a few events
			for j := 0; j < 2; j++ {
				select {
				case <-ch:
				case <-time.After(100 * time.Millisecond):
					return
				}
			}
		}()
	}

	wg.Wait()
}

func TestSessionRegistry_MessageChannel(t *testing.T) {
	reg := NewSessionRegistry(nil)
	reg.CreateSession("s1", "proj", "user", "/root", "linux", "")

	ch := reg.MessageChannel("s1")
	require.NotNil(t, ch)

	go func() {
		reg.EnqueueMessage("s1", "test-msg")
	}()

	select {
	case msg := <-ch:
		assert.Equal(t, "test-msg", msg)
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

func TestSessionRegistry_MessageChannel_NotFound(t *testing.T) {
	reg := NewSessionRegistry(nil)

	ch := reg.MessageChannel("nonexistent")
	_, ok := <-ch
	assert.False(t, ok)
}

// TC-G-02: SendMessage + Subscribe (pub/sub combined)
// CreateSession → Subscribe → EnqueueMessage → message arrives via MessageChannel
func TestSessionRegistry_SendMessageAndSubscribe(t *testing.T) {
	reg := NewSessionRegistry(nil)
	reg.CreateSession("s1", "proj", "user", "/root", "linux", "")

	// Subscribe for events
	eventCh, cleanup := reg.Subscribe("s1")
	defer cleanup()

	// Get message channel for user messages
	msgCh := reg.MessageChannel("s1")
	require.NotNil(t, msgCh)

	// Enqueue a user message
	err := reg.EnqueueMessage("s1", "hello from user")
	require.NoError(t, err)

	// Verify message arrives via message channel
	select {
	case msg := <-msgCh:
		assert.Equal(t, "hello from user", msg)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message")
	}

	// Publish an event (simulating agent response)
	reg.PublishEvent("s1", &pb.SessionEvent{
		EventId: "evt-1",
		Content: "agent response",
		Type:    pb.SessionEventType_SESSION_EVENT_ANSWER,
	})

	// Verify event arrives via subscriber channel
	select {
	case evt := <-eventCh:
		assert.Equal(t, "evt-1", evt.EventId)
		assert.Equal(t, "agent response", evt.Content)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

// TC-G-10: Multiple sessions — events are isolated
func TestSessionRegistry_MultipleSessions_Isolated(t *testing.T) {
	reg := NewSessionRegistry(nil)
	reg.CreateSession("session-A", "proj-a", "user-a", "/a", "linux", "")
	reg.CreateSession("session-B", "proj-b", "user-b", "/b", "windows", "")

	// Subscribe to both sessions
	chA, cleanupA := reg.Subscribe("session-A")
	defer cleanupA()

	chB, cleanupB := reg.Subscribe("session-B")
	defer cleanupB()

	// Publish event to session-A only
	reg.PublishEvent("session-A", &pb.SessionEvent{
		EventId: "evt-a-1",
		Content: "event for A",
	})

	// Publish event to session-B only
	reg.PublishEvent("session-B", &pb.SessionEvent{
		EventId: "evt-b-1",
		Content: "event for B",
	})

	// Verify session-A subscriber receives only A's event
	select {
	case evt := <-chA:
		assert.Equal(t, "evt-a-1", evt.EventId)
		assert.Equal(t, "event for A", evt.Content)
	case <-time.After(time.Second):
		t.Fatal("session-A: timed out waiting for event")
	}

	// Verify session-B subscriber receives only B's event
	select {
	case evt := <-chB:
		assert.Equal(t, "evt-b-1", evt.EventId)
		assert.Equal(t, "event for B", evt.Content)
	case <-time.After(time.Second):
		t.Fatal("session-B: timed out waiting for event")
	}

	// Verify no cross-contamination: session-A shouldn't have B's events
	select {
	case evt := <-chA:
		t.Fatalf("session-A received unexpected event: %s", evt.EventId)
	case <-time.After(100 * time.Millisecond):
		// Expected: no more events
	}

	// Verify no cross-contamination: session-B shouldn't have A's events
	select {
	case evt := <-chB:
		t.Fatalf("session-B received unexpected event: %s", evt.EventId)
	case <-time.After(100 * time.Millisecond):
		// Expected: no more events
	}

	// Verify messages are also isolated
	err := reg.EnqueueMessage("session-A", "msg for A")
	require.NoError(t, err)

	msgChB := reg.MessageChannel("session-B")
	select {
	case <-msgChB:
		t.Fatal("session-B received message intended for session-A")
	case <-time.After(100 * time.Millisecond):
		// Expected: no cross-contamination
	}

	msgChA := reg.MessageChannel("session-A")
	select {
	case msg := <-msgChA:
		assert.Equal(t, "msg for A", msg)
	case <-time.After(time.Second):
		t.Fatal("session-A: timed out waiting for message")
	}
}

// TC-G-08 extended: Reconnect replay with subscriber
// Publish 3 events → new subscriber with replay from evt-1 → receives events 2,3 + live events
func TestSessionRegistry_ReconnectReplay_WithSubscriber(t *testing.T) {
	store := newTestEventStore(t)
	reg := NewSessionRegistry(store)
	reg.CreateSession("s1", "proj", "user", "/root", "linux", "")

	// Append 3 events via store (no subscribers yet)
	var ids [3]string
	for i := 0; i < 3; i++ {
		id, err := store.Append("s1", "answer", &pb.SessionEvent{
			Content: fmt.Sprintf("msg-%d", i+1),
			Type:    pb.SessionEventType_SESSION_EVENT_ANSWER,
		}, map[string]interface{}{"type": "MessageCompleted", "content": fmt.Sprintf("msg-%d", i+1)})
		require.NoError(t, err)
		ids[i] = id
	}

	// Replay from first event → should get 2nd and 3rd
	replayed := reg.ReplayEvents("s1", ids[0])
	require.Len(t, replayed, 2)
	assert.Equal(t, "msg-2", replayed[0].Content)
	assert.Equal(t, "msg-3", replayed[1].Content)

	// Now subscribe for live events
	ch, cleanup := reg.Subscribe("s1")
	defer cleanup()

	// Publish a new event
	reg.PublishEvent("s1", &pb.SessionEvent{
		EventId: "evt-4",
		Content: "msg-4",
	})

	// Subscriber should receive the live event
	select {
	case evt := <-ch:
		assert.Equal(t, "evt-4", evt.EventId)
		assert.Equal(t, "msg-4", evt.Content)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for live event after replay")
	}
}

// TC-G-06 extended: AskUser register → reply → unregister lifecycle
func TestSessionRegistry_AskUser_FullLifecycle(t *testing.T) {
	reg := NewSessionRegistry(nil)
	reg.CreateSession("s1", "proj", "user", "/root", "linux", "")

	// Register ask_user call
	replyCh := reg.RegisterAskUser("s1", "call-42")

	// Send reply from another goroutine (simulating client)
	go func() {
		time.Sleep(10 * time.Millisecond)
		reg.SendAskUserReply("s1", "call-42", "yes, proceed")
	}()

	// Wait for reply
	select {
	case reply := <-replyCh:
		assert.Equal(t, "yes, proceed", reply)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ask_user reply")
	}

	// Unregister
	reg.UnregisterAskUser("s1", "call-42")

	// Sending reply after unregister should not panic or block
	reg.SendAskUserReply("s1", "call-42", "late reply")
}

// DrainMessages discards all pending messages from the queue
func TestSessionRegistry_DrainMessages(t *testing.T) {
	reg := NewSessionRegistry(nil)
	reg.CreateSession("s1", "proj", "user", "/root", "linux", "")

	// Enqueue several messages
	require.NoError(t, reg.EnqueueMessage("s1", "msg-1"))
	require.NoError(t, reg.EnqueueMessage("s1", "msg-2"))
	require.NoError(t, reg.EnqueueMessage("s1", "msg-3"))

	// Drain all messages
	reg.DrainMessages("s1")

	// Channel should be empty now
	ch := reg.MessageChannel("s1")
	select {
	case <-ch:
		t.Fatal("expected empty channel after drain")
	case <-time.After(50 * time.Millisecond):
		// Expected: no messages
	}
}

func TestSessionRegistry_DrainMessages_NotFound(t *testing.T) {
	reg := NewSessionRegistry(nil)
	// Should not panic on nonexistent session
	reg.DrainMessages("nonexistent")
}

// ResetCancel clears the cancelled flag
func TestSessionRegistry_ResetCancel(t *testing.T) {
	reg := NewSessionRegistry(nil)
	reg.CreateSession("s1", "proj", "user", "/root", "linux", "")

	// Cancel then reset
	reg.Cancel("s1")
	assert.True(t, reg.IsCancelled("s1"))

	reg.ResetCancel("s1")
	assert.False(t, reg.IsCancelled("s1"))
}

func TestSessionRegistry_ResetCancel_NotFound(t *testing.T) {
	reg := NewSessionRegistry(nil)
	// Should not panic
	reg.ResetCancel("nonexistent")
}

// StoreTurnCancel stores and invokes turn cancel function
func TestSessionRegistry_StoreTurnCancel(t *testing.T) {
	reg := NewSessionRegistry(nil)
	reg.CreateSession("s1", "proj", "user", "/root", "linux", "")

	cancelled := make(chan struct{})
	_, cancel := context.WithCancel(context.Background())

	// Wrap cancel to detect invocation
	reg.StoreTurnCancel("s1", func() {
		cancel()
		close(cancelled)
	})

	// Cancel session should invoke turnCancelFn
	reg.Cancel("s1")

	select {
	case <-cancelled:
		// Expected: turn cancel was invoked
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for turn cancel invocation")
	}
}

func TestSessionRegistry_StoreTurnCancel_ClearNil(t *testing.T) {
	reg := NewSessionRegistry(nil)
	reg.CreateSession("s1", "proj", "user", "/root", "linux", "")

	called := false
	reg.StoreTurnCancel("s1", func() { called = true })

	// Clear the cancel func
	reg.StoreTurnCancel("s1", nil)

	// Cancel should NOT invoke nil func (no panic)
	reg.Cancel("s1")
	assert.False(t, called)
}

func TestSessionRegistry_StoreTurnCancel_NotFound(t *testing.T) {
	reg := NewSessionRegistry(nil)
	// Should not panic
	reg.StoreTurnCancel("nonexistent", func() {})
}

// TC-EB-01: SetEventHook → PublishEvent → hook called with correct args.
func TestSessionRegistry_EventHook_Called(t *testing.T) {
	reg := NewSessionRegistry(nil)

	var hookCalled bool
	var hookSessionID string
	var hookEvent *pb.SessionEvent

	reg.SetEventHook(func(sessionID string, event *pb.SessionEvent) {
		hookCalled = true
		hookSessionID = sessionID
		hookEvent = event
	})

	reg.CreateSession("s1", "key", "user", "/root", "linux", "")

	event := &pb.SessionEvent{EventId: "e1", Content: "test"}
	reg.PublishEvent("s1", event)

	assert.True(t, hookCalled)
	assert.Equal(t, "s1", hookSessionID)
	assert.Equal(t, "e1", hookEvent.EventId)
	assert.Equal(t, "test", hookEvent.Content)
}

// TC-EB-02: No hook → PublishEvent still works (subscribers receive events).
func TestSessionRegistry_EventHook_NilDoesNotBreak(t *testing.T) {
	reg := NewSessionRegistry(nil)
	// No SetEventHook call — hook is nil

	reg.CreateSession("s1", "key", "user", "/root", "linux", "")

	ch, cleanup := reg.Subscribe("s1")
	defer cleanup()

	event := &pb.SessionEvent{EventId: "e1", Content: "hello"}
	reg.PublishEvent("s1", event)

	select {
	case received := <-ch:
		assert.Equal(t, "e1", received.EventId)
		assert.Equal(t, "hello", received.Content)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event without hook")
	}
}

// TC-EB-03: EventHook called for every event (multiple events).
func TestSessionRegistry_EventHook_MultipleEvents(t *testing.T) {
	reg := NewSessionRegistry(nil)

	var hookEvents []*pb.SessionEvent

	reg.SetEventHook(func(_ string, event *pb.SessionEvent) {
		hookEvents = append(hookEvents, event)
	})

	reg.CreateSession("s1", "key", "user", "/root", "linux", "")

	for i := 1; i <= 3; i++ {
		reg.PublishEvent("s1", &pb.SessionEvent{
			EventId: fmt.Sprintf("evt-%d", i),
			Content: fmt.Sprintf("msg-%d", i),
		})
	}

	require.Len(t, hookEvents, 3)
	assert.Equal(t, "evt-1", hookEvents[0].EventId)
	assert.Equal(t, "evt-2", hookEvents[1].EventId)
	assert.Equal(t, "evt-3", hookEvents[2].EventId)
}

// TC-G-07 extended: Cancel prevents further processing
func TestSessionRegistry_Cancel_MultipleCalls(t *testing.T) {
	reg := NewSessionRegistry(nil)
	reg.CreateSession("s1", "proj", "user", "/root", "linux", "")

	// Initially not cancelled
	assert.False(t, reg.IsCancelled("s1"))

	// Cancel
	ok := reg.Cancel("s1")
	assert.True(t, ok)
	assert.True(t, reg.IsCancelled("s1"))

	// Cancel again — idempotent
	ok = reg.Cancel("s1")
	assert.True(t, ok)
	assert.True(t, reg.IsCancelled("s1"))

	// Nonexistent session
	assert.False(t, reg.IsCancelled("nonexistent"))
}
