package app

import (
	"context"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
	"github.com/syntheticinc/syntheticbrew/internal/usecase/agentrelationcreate"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// agentRelationCreateRepoAdapter implements agentrelationcreate.RelationRepository
// over an agentRelationLister (the GORM repository in production, a fake in
// tests). It owns the persistence-boundary concern of mapping records and
// translating driver FK / duplicate-key failures into typed DomainErrors, so
// the usecase and every facade above it never string-match driver messages.
type agentRelationCreateRepoAdapter struct {
	repo agentRelationLister
}

func (a *agentRelationCreateRepoAdapter) List(ctx context.Context, schemaID string) ([]agentrelationcreate.Relation, error) {
	records, err := a.repo.List(ctx, schemaID)
	if err != nil {
		return nil, err
	}
	out := make([]agentrelationcreate.Relation, 0, len(records))
	for _, r := range records {
		out = append(out, agentrelationcreate.Relation{
			ID:            r.ID,
			SchemaID:      r.SchemaID,
			SourceAgentID: r.SourceAgentID,
			TargetAgentID: r.TargetAgentID,
			Config:        r.Config,
		})
	}
	return out, nil
}

func (a *agentRelationCreateRepoAdapter) Create(ctx context.Context, r *agentrelationcreate.Relation) error {
	rec := &configrepo.AgentRelationRecord{
		SchemaID:      r.SchemaID,
		SourceAgentID: r.SourceAgentID,
		TargetAgentID: r.TargetAgentID,
		Config:        r.Config,
	}
	if err := a.repo.Create(ctx, rec); err != nil {
		if isAgentRelationFKViolation(err) {
			return pkgerrors.NotFound("source or target agent no longer exists (deleted concurrently)")
		}
		if isDuplicateKeyErr(err) {
			return pkgerrors.AlreadyExists("agent relation between these agents already exists in this schema")
		}
		return err
	}
	r.ID = rec.ID
	return nil
}

// newAgentRelationCreateUsecase assembles the guarded agent-relation-create
// usecase — the single seam every relation-creation facade (REST + MCP) routes
// through, so the self-loop / acyclicity invariants cannot be skipped by any
// path.
func newAgentRelationCreateUsecase(repo agentRelationLister) *agentrelationcreate.Usecase {
	return agentrelationcreate.New(&agentRelationCreateRepoAdapter{repo: repo})
}
