package app

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
	admintools "github.com/syntheticinc/syntheticbrew/internal/infrastructure/tools/admin"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// newAdminRelationCycleAdapter wires the MCP relation adapter with fakes. The
// create-seam usecase shares the same fake lister, so the cycle graph and the
// created edges are one dataset — mirrors production where both point at the
// same agent_relations table.
func newAdminRelationCycleAdapter() *adminAgentRelationRepoAdapter {
	lister := newFakeRelationLister(schemaUUID)
	return &adminAgentRelationRepoAdapter{
		repo:      lister,
		agentRepo: &fakeAgentResolver{byName: map[string]*configrepo.AgentRecord{}},
		creator:   newAgentRelationCreateUsecase(lister),
	}
}

// Test_AdminCreateAgentRelation_SelfLoopRejected: the MCP admin tool must
// reject a self-loop (helper→helper) exactly like the REST path.
func Test_AdminCreateAgentRelation_SelfLoopRejected(t *testing.T) {
	a := newAdminRelationCycleAdapter()
	err := a.Create(context.Background(), &admintools.AgentRelationRecord{
		SchemaID: schemaUUID, FromAgent: agentAUUID, ToAgent: agentAUUID,
	})
	require.Error(t, err)
	var de *pkgerrors.DomainError
	require.True(t, errors.As(err, &de), "expected DomainError, got %T", err)
	assert.Equal(t, pkgerrors.CodeInvalidInput, de.Code)
}

// Test_AdminCreateAgentRelation_TwoCycleRejected: A→B then B→A closes a 2-cycle
// and must be rejected on the MCP path (BUG-01: it previously went straight to
// the repo, bypassing the cycle guard the REST path enforces).
func Test_AdminCreateAgentRelation_TwoCycleRejected(t *testing.T) {
	a := newAdminRelationCycleAdapter()
	require.NoError(t, a.Create(context.Background(), &admintools.AgentRelationRecord{
		SchemaID: schemaUUID, FromAgent: agentAUUID, ToAgent: agentBUUID,
	}))
	err := a.Create(context.Background(), &admintools.AgentRelationRecord{
		SchemaID: schemaUUID, FromAgent: agentBUUID, ToAgent: agentAUUID,
	})
	require.Error(t, err, "MCP admin_create_agent_relation must reject a 2-cycle like REST")
	var de *pkgerrors.DomainError
	require.True(t, errors.As(err, &de), "expected DomainError, got %T", err)
	assert.Equal(t, pkgerrors.CodeInvalidInput, de.Code)
}
