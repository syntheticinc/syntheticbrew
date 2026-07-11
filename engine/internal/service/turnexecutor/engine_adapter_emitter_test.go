package turnexecutor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

type recordingHistoryRepo struct {
	called     bool
	lastTenant string
	lastType   domain.MessageType
}

func (r *recordingHistoryRepo) Create(ctx context.Context, m *domain.Message) error {
	r.called = true
	r.lastTenant = domain.TenantIDFromContext(ctx)
	r.lastType = m.Type
	return nil
}

// Regression for the multi-tenant bug "interrupt_request missing from history": the
// tool-emitted interrupt_request/resume are mirrored to the messages table by
// eventCallbackEmitter. It used context.Background() → CE default tenant → a
// multi-tenant tenant-scoped GET /sessions/{id}/messages excluded the
// row, so the widget vanished on reload. The mirror must use the turn tenant.
func TestEventCallbackEmitter_InterruptHistoryUsesTurnTenant(t *testing.T) {
	const tenant = "11111111-1111-1111-1111-111111111111"
	repo := &recordingHistoryRepo{}
	em := &eventCallbackEmitter{
		ctx:         domain.WithTenantID(context.Background(), tenant),
		historyRepo: repo,
		sessionID:   "session-1",
		agentID:     "agent-1",
	}

	err := em.Send(&domain.AgentEvent{
		Type:     domain.EventTypeInterruptRequest,
		Content:  `{"interrupt_id":"int-1"}`,
		Metadata: map[string]interface{}{"interrupt_id": "int-1"},
	})
	require.NoError(t, err)
	require.True(t, repo.called, "interrupt_request must be mirrored to history")
	assert.Equal(t, domain.MessageTypeInterruptRequest, repo.lastType)
	assert.Equal(t, tenant, repo.lastTenant,
		"interrupt history mirror must use the turn tenant, not context.Background()")
}
