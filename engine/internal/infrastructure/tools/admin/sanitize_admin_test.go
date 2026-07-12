package admin

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// rawPGTokens are internal database identifiers that must never appear in a
// tool result surfaced to an MCP client or the LLM.
var rawPGTokens = []string{"constraint", "SQLSTATE", "chk_", "idx_", "fk_", "violates", "23505", "23514", "23503"}

func assertNoRawPG(t *testing.T, out string) {
	t.Helper()
	for _, tok := range rawPGTokens {
		if strings.Contains(out, tok) {
			t.Fatalf("tool result leaked raw DB token %q: %s", tok, out)
		}
	}
}

// failAgentRepo returns preconfigured errors so the not-found and hard-DB-error
// branches of the admin tools can be driven without a real database.
type failAgentRepo struct {
	getErr    error
	createErr error
	deleteErr error
}

func (r *failAgentRepo) List(context.Context) ([]AgentRecord, error) { return nil, nil }
func (r *failAgentRepo) GetByName(context.Context, string) (*AgentRecord, error) {
	return nil, r.getErr
}
func (r *failAgentRepo) Create(context.Context, *AgentRecord) error         { return r.createErr }
func (r *failAgentRepo) Update(context.Context, string, *AgentRecord) error { return nil }
func (r *failAgentRepo) Delete(context.Context, string) error               { return r.deleteErr }

// failSchemaRepo returns a preconfigured Delete error for the not-found branch.
type failSchemaRepo struct {
	deleteErr error
}

func (r *failSchemaRepo) List(context.Context) ([]SchemaRecord, error)           { return nil, nil }
func (r *failSchemaRepo) GetByID(context.Context, string) (*SchemaRecord, error) { return nil, nil }
func (r *failSchemaRepo) Create(context.Context, *SchemaRecord) error            { return nil }
func (r *failSchemaRepo) Update(context.Context, string, *SchemaRecord) error    { return nil }
func (r *failSchemaRepo) Delete(context.Context, string) error                   { return r.deleteErr }

const (
	notFoundErrText = "record not found"
	rawUniqueErr    = `ERROR: duplicate key value violates unique constraint "idx_agents_name" (SQLSTATE 23505)`
	rawCheckErr     = `ERROR: new row for relation "agents" violates check constraint "chk_agents_lifecycle" (SQLSTATE 23514)`
)

// TestAdminTool_NotFoundIsMarked pins BUG B: a not-found tool result must carry
// the [ERROR] marker so the MCP bridge maps it to isError:true, while keeping
// the human "not found" wording.
func TestAdminTool_NotFoundIsMarked(t *testing.T) {
	ctx := context.Background()

	getAgent := &adminGetAgentTool{repo: &failAgentRepo{getErr: errors.New(notFoundErrText)}}
	out, err := getAgent.InvokableRun(ctx, `{"name":"ghost"}`)
	if err != nil {
		t.Fatalf("unexpected go error: %v", err)
	}
	if !strings.HasPrefix(out, "[ERROR]") {
		t.Fatalf("not-found agent result must be [ERROR]-marked, got: %s", out)
	}
	if !strings.Contains(out, "not found") {
		t.Fatalf("expected human 'not found' wording, got: %s", out)
	}

	delSchema := &adminDeleteSchemaTool{repo: &failSchemaRepo{deleteErr: errors.New(notFoundErrText)}}
	out, err = delSchema.InvokableRun(ctx, `{"schema_id":"missing"}`)
	if err != nil {
		t.Fatalf("unexpected go error: %v", err)
	}
	if !strings.HasPrefix(out, "[ERROR]") {
		t.Fatalf("not-found schema result must be [ERROR]-marked, got: %s", out)
	}
}

// TestAdminTool_DBErrorIsSanitized pins BUG C: a hard DB failure must be
// [ERROR]-marked AND stripped of raw Postgres identifiers before it reaches the
// client.
func TestAdminTool_DBErrorIsSanitized(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name       string
		run        func() (string, error)
		wantPhrase string
	}{
		{
			name: "get_agent unique violation",
			run: func() (string, error) {
				tl := &adminGetAgentTool{repo: &failAgentRepo{getErr: errors.New(rawUniqueErr)}}
				return tl.InvokableRun(ctx, `{"name":"x"}`)
			},
			wantPhrase: "already exists",
		},
		{
			name: "create_agent check violation",
			run: func() (string, error) {
				tl := &adminCreateAgentTool{
					repo:     &failAgentRepo{createErr: errors.New(rawCheckErr)},
					reloader: func(context.Context) {},
				}
				return tl.InvokableRun(ctx, `{"name":"ok-name","system_prompt":"p"}`)
			},
			wantPhrase: "one or more fields have an invalid value",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := tc.run()
			if err != nil {
				t.Fatalf("unexpected go error: %v", err)
			}
			if !strings.HasPrefix(out, "[ERROR]") {
				t.Fatalf("DB-failure result must be [ERROR]-marked, got: %s", out)
			}
			assertNoRawPG(t, out)
			if !strings.Contains(out, tc.wantPhrase) {
				t.Fatalf("expected sanitized phrase %q, got: %s", tc.wantPhrase, out)
			}
		})
	}
}
