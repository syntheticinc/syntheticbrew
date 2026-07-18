package app

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	deliveryhttp "github.com/syntheticinc/syntheticbrew/internal/delivery/http"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// fakeRelationLister is an in-memory stand-in for
// *configrepo.GORMAgentRelationRepository that exercises the cycle-check path
// without a database. It tracks a single schema's relations keyed by ID and is
// append/update-only (Delete is the identity for the scenarios below).
type fakeRelationLister struct {
	byID     map[string]configrepo.AgentRelationRecord
	schemaID string
	nextID   int
}

func newFakeRelationLister(schemaID string) *fakeRelationLister {
	return &fakeRelationLister{
		byID:     map[string]configrepo.AgentRelationRecord{},
		schemaID: schemaID,
	}
}

func (f *fakeRelationLister) List(_ context.Context, schemaID string) ([]configrepo.AgentRelationRecord, error) {
	out := make([]configrepo.AgentRelationRecord, 0, len(f.byID))
	for _, r := range f.byID {
		if r.SchemaID == schemaID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeRelationLister) GetByID(_ context.Context, id string) (*configrepo.AgentRelationRecord, error) {
	if r, ok := f.byID[id]; ok {
		return &r, nil
	}
	return nil, errors.New("not found")
}

func (f *fakeRelationLister) Create(_ context.Context, r *configrepo.AgentRelationRecord) error {
	f.nextID++
	r.ID = generateFakeID(f.nextID)
	f.byID[r.ID] = *r
	return nil
}

func (f *fakeRelationLister) Update(_ context.Context, id string, r *configrepo.AgentRelationRecord) error {
	existing, ok := f.byID[id]
	if !ok {
		return errors.New("not found")
	}
	existing.SourceAgentID = r.SourceAgentID
	existing.TargetAgentID = r.TargetAgentID
	existing.Config = r.Config
	f.byID[id] = existing
	return nil
}

func (f *fakeRelationLister) Delete(_ context.Context, id string) error {
	delete(f.byID, id)
	return nil
}

func generateFakeID(n int) string {
	return "rel-" + string(rune('0'+n))
}

// fakeAgentResolver resolves agent UUID references through a fixed map. The
// cycle tests use UUIDs directly (resolveAgentRef short-circuits on UUIDs) so
// this is only used for the "by name" branch — we keep the interface satisfied
// for completeness but the tests don't exercise it.
type fakeAgentResolver struct {
	byName map[string]*configrepo.AgentRecord
}

func (f *fakeAgentResolver) GetByName(_ context.Context, name string) (*configrepo.AgentRecord, error) {
	if r, ok := f.byName[name]; ok {
		return r, nil
	}
	return nil, errors.New("agent not found")
}

func (f *fakeAgentResolver) List(_ context.Context) ([]configrepo.AgentRecord, error) {
	out := make([]configrepo.AgentRecord, 0, len(f.byName))
	for _, r := range f.byName {
		out = append(out, *r)
	}
	return out, nil
}

// fakeSchemaTenantChecker returns a fixed schema record. Tests seed a single
// schema belonging to the same "tenant" and return it for every lookup; cross-
// tenant isolation is covered by tenant_isolation_test.go at the repo layer,
// not here.
type fakeSchemaTenantChecker struct {
	record *configrepo.SchemaRecord
}

func (f *fakeSchemaTenantChecker) GetByID(_ context.Context, id string) (*configrepo.SchemaRecord, error) {
	if f.record != nil && f.record.ID == id {
		return f.record, nil
	}
	return nil, errors.New("schema not found")
}

func (f *fakeSchemaTenantChecker) Update(_ context.Context, id string, r *configrepo.SchemaRecord) error {
	if f.record == nil || f.record.ID != id {
		return errors.New("schema not found")
	}
	f.record.EntryAgentID = r.EntryAgentID
	return nil
}

// Canonical UUIDs for the three agents used in the scenarios below. isUUID()
// short-circuits resolveAgentRef so these flow through unchanged.
const (
	agentAUUID = "00000000-0000-0000-0000-00000000000a"
	agentBUUID = "00000000-0000-0000-0000-00000000000b"
	agentCUUID = "00000000-0000-0000-0000-00000000000c"
	agentDUUID = "00000000-0000-0000-0000-00000000000d"
	schemaUUID = "11111111-1111-1111-1111-111111111111"
)

// newCycleAdapter wires the relation adapter with fakes, pre-populating the
// schema record so CreateAgentRelation's schemaRepo.GetByID succeeds.
func newCycleAdapter(t *testing.T) (*agentRelationServiceHTTPAdapter, *fakeRelationLister) {
	t.Helper()
	lister := newFakeRelationLister(schemaUUID)
	return &agentRelationServiceHTTPAdapter{
		repo:       lister,
		agentRepo:  &fakeAgentResolver{byName: map[string]*configrepo.AgentRecord{}},
		schemaRepo: &fakeSchemaTenantChecker{record: &configrepo.SchemaRecord{ID: schemaUUID, Name: "s1"}},
		// db is not used by any code path exercised here (all lookups go
		// through the interface fields), so leaving it nil is safe and
		// makes it explicit that the cycle check is DB-free.
		db: nil,
		// create + cycle guard route through the shared seam over the same fake
		// lister, so List (cycle graph) and Create see one dataset.
		creator: newAgentRelationCreateUsecase(lister),
	}, lister
}

// Test_CreateAgentRelation_SimpleTwoCycle: Bug 2 scenario — A→B exists, adding
// B→A closes a 2-cycle and must be rejected with InvalidInput.
func Test_CreateAgentRelation_SimpleTwoCycle(t *testing.T) {
	a, _ := newCycleAdapter(t)
	ctx := context.Background()

	// First edge A→B is accepted.
	_, err := a.CreateAgentRelation(ctx, schemaUUID, deliveryhttp.CreateAgentRelationRequest{
		Source: agentAUUID,
		Target: agentBUUID,
	})
	require.NoError(t, err)

	// Closing B→A must fail with cycle-detection InvalidInput.
	_, err = a.CreateAgentRelation(ctx, schemaUUID, deliveryhttp.CreateAgentRelationRequest{
		Source: agentBUUID,
		Target: agentAUUID,
	})
	require.Error(t, err)
	var de *pkgerrors.DomainError
	require.True(t, errors.As(err, &de), "expected DomainError, got %T", err)
	assert.Equal(t, pkgerrors.CodeInvalidInput, de.Code)
	assert.Contains(t, de.Message, "circular")
}

// Test_CreateAgentRelation_ThreeCycle: A→B→C exists, adding C→A closes the
// 3-cycle A→B→C→A through transitive reachability.
func Test_CreateAgentRelation_ThreeCycle(t *testing.T) {
	a, _ := newCycleAdapter(t)
	ctx := context.Background()

	_, err := a.CreateAgentRelation(ctx, schemaUUID, deliveryhttp.CreateAgentRelationRequest{
		Source: agentAUUID, Target: agentBUUID,
	})
	require.NoError(t, err)
	_, err = a.CreateAgentRelation(ctx, schemaUUID, deliveryhttp.CreateAgentRelationRequest{
		Source: agentBUUID, Target: agentCUUID,
	})
	require.NoError(t, err)

	_, err = a.CreateAgentRelation(ctx, schemaUUID, deliveryhttp.CreateAgentRelationRequest{
		Source: agentCUUID, Target: agentAUUID,
	})
	require.Error(t, err)
	var de *pkgerrors.DomainError
	require.True(t, errors.As(err, &de))
	assert.Equal(t, pkgerrors.CodeInvalidInput, de.Code)
}

// Test_CreateAgentRelation_SelfLoopRejected: A→A is rejected regardless of
// the cycle check (caught earlier by the "source and target must differ"
// guard, but the adapter must still return InvalidInput — not 500 or similar).
func Test_CreateAgentRelation_SelfLoopRejected(t *testing.T) {
	a, _ := newCycleAdapter(t)
	ctx := context.Background()

	_, err := a.CreateAgentRelation(ctx, schemaUUID, deliveryhttp.CreateAgentRelationRequest{
		Source: agentAUUID, Target: agentAUUID,
	})
	require.Error(t, err)
	var de *pkgerrors.DomainError
	require.True(t, errors.As(err, &de))
	assert.Equal(t, pkgerrors.CodeInvalidInput, de.Code)
}

// Test_CreateAgentRelation_TreeNoFalsePositives: A→B, A→C, A→D (branching out)
// contains no cycle. Every edge must be accepted.
func Test_CreateAgentRelation_TreeNoFalsePositives(t *testing.T) {
	a, _ := newCycleAdapter(t)
	ctx := context.Background()

	for _, target := range []string{agentBUUID, agentCUUID, agentDUUID} {
		_, err := a.CreateAgentRelation(ctx, schemaUUID, deliveryhttp.CreateAgentRelationRequest{
			Source: agentAUUID, Target: target,
		})
		require.NoError(t, err, "A→%s should be accepted (no cycle)", target)
	}
}

// Test_UpdateAgentRelation_ExcludesSelfFromCycleCheck: updating an existing
// edge A→B to A→B (a no-op rewrite, or rewriting config) must NOT be reported
// as a cycle through itself. checkNoCycleExcluding takes the current edge ID
// and drops it from the adjacency list before probing reachability.
func Test_UpdateAgentRelation_ExcludesSelfFromCycleCheck(t *testing.T) {
	a, lister := newCycleAdapter(t)
	ctx := context.Background()

	_, err := a.CreateAgentRelation(ctx, schemaUUID, deliveryhttp.CreateAgentRelationRequest{
		Source: agentAUUID, Target: agentBUUID,
	})
	require.NoError(t, err)

	var relID string
	for id := range lister.byID {
		relID = id
	}
	require.NotEmpty(t, relID)

	// Rewrite the edge to A→B again — same endpoints, same schema.
	err = a.UpdateAgentRelation(ctx, relID, deliveryhttp.CreateAgentRelationRequest{
		Source: agentAUUID, Target: agentBUUID,
	})
	require.NoError(t, err, "updating edge to same endpoints must not self-report a cycle")
}

// Test_UpdateAgentRelation_ClosesCycleAfterChange: A→B and C→D exist; updating
// the (A→B) edge to (B→A) — effectively introducing a 2-cycle with existing
// A→B's counterpart — must be rejected even though the rewrite is self-
// exclusion-aware (the other direction must still be in the graph).
//
// Setup: existing edges = {A→B, B→A-candidate (would close cycle with A→B)}.
// We do this by first creating A→B, then attempting to UPDATE a separate
// C→D edge to B→A, verifying cycle detection still fires.
func Test_UpdateAgentRelation_ClosesCycleAfterChange(t *testing.T) {
	a, lister := newCycleAdapter(t)
	ctx := context.Background()

	// Existing A→B.
	_, err := a.CreateAgentRelation(ctx, schemaUUID, deliveryhttp.CreateAgentRelationRequest{
		Source: agentAUUID, Target: agentBUUID,
	})
	require.NoError(t, err)

	// Existing C→D (unrelated).
	_, err = a.CreateAgentRelation(ctx, schemaUUID, deliveryhttp.CreateAgentRelationRequest{
		Source: agentCUUID, Target: agentDUUID,
	})
	require.NoError(t, err)

	// Find C→D's ID so we can update it.
	var cdID string
	for id, rel := range lister.byID {
		if rel.SourceAgentID == agentCUUID && rel.TargetAgentID == agentDUUID {
			cdID = id
		}
	}
	require.NotEmpty(t, cdID)

	// Rewriting C→D to B→A must fail because A→B is still present and would
	// close a 2-cycle.
	err = a.UpdateAgentRelation(ctx, cdID, deliveryhttp.CreateAgentRelationRequest{
		Source: agentBUUID, Target: agentAUUID,
	})
	require.Error(t, err)
	var de *pkgerrors.DomainError
	require.True(t, errors.As(err, &de))
	assert.Equal(t, pkgerrors.CodeInvalidInput, de.Code)
}

// Test_CreateAgentRelation_LongerChainNoFalsePositive: A→B→C→D exists. Adding
// A→D (shortcut) must be accepted — no path D→A exists.
func Test_CreateAgentRelation_LongerChainNoFalsePositive(t *testing.T) {
	a, _ := newCycleAdapter(t)
	ctx := context.Background()

	edges := [][2]string{
		{agentAUUID, agentBUUID},
		{agentBUUID, agentCUUID},
		{agentCUUID, agentDUUID},
	}
	for _, e := range edges {
		_, err := a.CreateAgentRelation(ctx, schemaUUID, deliveryhttp.CreateAgentRelationRequest{
			Source: e[0], Target: e[1],
		})
		require.NoError(t, err)
	}

	// A→D shortcut is fine; no cycle.
	_, err := a.CreateAgentRelation(ctx, schemaUUID, deliveryhttp.CreateAgentRelationRequest{
		Source: agentAUUID, Target: agentDUUID,
	})
	require.NoError(t, err)
}
