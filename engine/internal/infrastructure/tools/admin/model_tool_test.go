package admin

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeModelRepo struct {
	created *ModelRecord
	updated *ModelRecord
	byID    map[string]*ModelRecord
}

func newFakeModelRepo() *fakeModelRepo { return &fakeModelRepo{byID: map[string]*ModelRecord{}} }

func (f *fakeModelRepo) List(context.Context) ([]ModelRecord, error) { return nil, nil }
func (f *fakeModelRepo) GetByID(_ context.Context, id string) (*ModelRecord, error) {
	if r, ok := f.byID[id]; ok {
		return r, nil
	}
	return nil, errors.New("not found")
}
func (f *fakeModelRepo) Create(_ context.Context, r *ModelRecord) error {
	r.ID = "m1"
	f.created = r
	return nil
}
func (f *fakeModelRepo) Update(_ context.Context, _ string, r *ModelRecord) error {
	f.updated = r
	return nil
}
func (f *fakeModelRepo) Delete(context.Context, string) error             { return nil }
func (f *fakeModelRepo) GetDefault(context.Context) (*ModelRecord, error) { return nil, nil }
func (f *fakeModelRepo) SetDefault(context.Context, string) error         { return nil }

func runCreateModel(t *testing.T, repo ModelRepository, args string) string {
	t.Helper()
	out, err := NewAdminCreateModelTool(repo, nil).InvokableRun(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected transport err: %v", err)
	}
	return out
}

// admin_create_model must apply the same type-enum + openrouter canonicalization
// + base_url validation the REST handler does (BUG-01 class: the MCP path
// previously wrote straight to the repo, bypassing all three).

func TestAdminCreateModel_CanonicalizesOpenRouter(t *testing.T) {
	repo := newFakeModelRepo()
	out := runCreateModel(t, repo, `{"name":"m","type":"openrouter","model_name":"x","api_key":"k"}`)
	if repo.created == nil {
		t.Fatalf("expected a create, got none; out=%s", out)
	}
	if repo.created.Type != "openai_compatible" {
		t.Fatalf("openrouter must canonicalize to openai_compatible, got %q", repo.created.Type)
	}
	if repo.created.BaseURL != "https://openrouter.ai/api/v1" {
		t.Fatalf("openrouter default base_url missing, got %q", repo.created.BaseURL)
	}
}

func TestAdminCreateModel_RejectsInvalidType(t *testing.T) {
	repo := newFakeModelRepo()
	out := runCreateModel(t, repo, `{"name":"m","type":"totally_bogus","model_name":"x"}`)
	if repo.created != nil {
		t.Fatalf("invalid type must be rejected before persist; created=%+v", repo.created)
	}
	if !strings.Contains(out, "type must be one of") {
		t.Fatalf("want type-enum error, got %s", out)
	}
}

func TestAdminCreateModel_RejectsBadBaseURL(t *testing.T) {
	repo := newFakeModelRepo()
	out := runCreateModel(t, repo, `{"name":"m","type":"openai_compatible","model_name":"x","base_url":"not-a-url"}`)
	if repo.created != nil {
		t.Fatalf("bad base_url must be rejected before persist; created=%+v", repo.created)
	}
	if !strings.Contains(out, "base_url") {
		t.Fatalf("want base_url error, got %s", out)
	}
}
