// Package agentrelationcreate is the single business-logic seam for creating
// agent-relation (delegation) edges. Every facade — the REST handler, the MCP
// admin tool, any future entry point — routes through Execute so the
// delegation invariants (no self-loop, no cycle) hold on every path. The
// symmetric CheckNoCycle is exposed for the update path, which mutates an
// existing edge rather than creating one.
package agentrelationcreate

import (
	"context"
	"fmt"

	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// RelationRepository lists a schema's existing relations (for cycle detection)
// and persists a new one. Implemented by an app-layer adapter over the GORM
// repository, which translates driver FK/duplicate failures into typed
// DomainErrors so the usecase and every facade never string-match driver text.
type RelationRepository interface {
	List(ctx context.Context, schemaID string) ([]Relation, error)
	Create(ctx context.Context, r *Relation) error
}

// Relation is the usecase-boundary shape of an agent-relation row. ID is set by
// the repository on successful insert.
type Relation struct {
	ID            string
	SchemaID      string
	SourceAgentID string
	TargetAgentID string
	Config        map[string]interface{}
}

// Input is a resolved create request: callers resolve agent name→UUID before
// invoking Execute, so the usecase owns only the delegation invariants.
type Input struct {
	SchemaID      string
	SourceAgentID string
	TargetAgentID string
	Config        map[string]interface{}
}

// Usecase creates agent relations behind the self-loop + acyclicity invariants.
type Usecase struct {
	repo RelationRepository
}

// New creates the agent-relation-create usecase.
func New(repo RelationRepository) *Usecase {
	return &Usecase{repo: repo}
}

// Execute creates an agent relation: reject self-loop → reject cycle → persist.
func (u *Usecase) Execute(ctx context.Context, in Input) (*Relation, error) {
	if in.SourceAgentID == in.TargetAgentID {
		return nil, pkgerrors.InvalidInput("source and target must be different agents")
	}
	if err := u.CheckNoCycle(ctx, in.SchemaID, in.SourceAgentID, in.TargetAgentID, ""); err != nil {
		return nil, err
	}
	rec := &Relation{
		SchemaID:      in.SchemaID,
		SourceAgentID: in.SourceAgentID,
		TargetAgentID: in.TargetAgentID,
		Config:        in.Config,
	}
	if err := u.repo.Create(ctx, rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// CheckNoCycle returns an InvalidInput error when adding source→target to the
// schema's existing delegation graph would close a cycle — i.e. a path
// target→…→source already exists. excludeID drops one existing relation from
// the graph (used by updates so re-saving an edge does not self-report a cycle
// through itself).
func (u *Usecase) CheckNoCycle(ctx context.Context, schemaID, sourceID, targetID, excludeID string) error {
	if sourceID == targetID {
		return pkgerrors.InvalidInput("source and target must be different agents")
	}

	existing, err := u.repo.List(ctx, schemaID)
	if err != nil {
		return fmt.Errorf("list existing agent relations: %w", err)
	}

	// Build an adjacency list of current edges (excluding the one being
	// updated, if any) and probe reachability from target back to source.
	adj := make(map[string][]string, len(existing))
	for _, r := range existing {
		if excludeID != "" && r.ID == excludeID {
			continue
		}
		adj[r.SourceAgentID] = append(adj[r.SourceAgentID], r.TargetAgentID)
	}

	if reachable(adj, targetID, sourceID) {
		return pkgerrors.InvalidInput("circular delegation: adding this edge would close a cycle")
	}
	return nil
}

// reachable reports whether dst is reachable from src in the directed graph adj.
// BFS; stops as soon as dst is dequeued. O(V+E). Tolerates existing cycles in
// the graph without looping (visited set).
func reachable(adj map[string][]string, src, dst string) bool {
	if src == dst {
		return true
	}
	visited := map[string]struct{}{src: {}}
	queue := []string{src}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range adj[cur] {
			if next == dst {
				return true
			}
			if _, seen := visited[next]; seen {
				continue
			}
			visited[next] = struct{}{}
			queue = append(queue, next)
		}
	}
	return false
}
