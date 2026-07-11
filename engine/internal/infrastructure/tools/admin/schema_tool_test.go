package admin

import (
	"context"
	"strings"
	"testing"

	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

func TestAdminCreateSchema_Success(t *testing.T) {
	sr := &stubSchemaRepo{}
	reloaded := false
	tool := &adminCreateSchemaTool{creator: &stubCreator{repo: sr}, reloader: func(context.Context) { reloaded = true }}

	out, err := tool.InvokableRun(context.Background(), `{"name":"reports","description":"d"}`)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.Contains(out, `Schema "reports" created`) {
		t.Fatalf("expected success render, got: %s", out)
	}
	if sr.created == nil || sr.created.Name != "reports" {
		t.Fatal("schema not persisted")
	}
	if !reloaded {
		t.Fatal("reloader not called after successful create")
	}
}

// TestAdminCreateSchema_QuotaRejection pins the tool path of the quota fix: a
// usage-limited creator surfaces the machine-readable sentinel and persists
// nothing — admin_create_schema is gated exactly like REST and provisioning.
func TestAdminCreateSchema_QuotaRejection(t *testing.T) {
	sr := &stubSchemaRepo{}
	reloaded := false
	tool := &adminCreateSchemaTool{
		creator:  &stubCreator{repo: sr, err: pkgerrors.UsageLimited("schema limit reached")},
		reloader: func(context.Context) { reloaded = true },
	}

	out, err := tool.InvokableRun(context.Background(), `{"name":"reports"}`)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.Contains(out, "[quota:schema_limit_reached]") {
		t.Fatalf("expected quota sentinel, got: %s", out)
	}
	if sr.created != nil {
		t.Fatal("nothing must be persisted on quota rejection")
	}
	if reloaded {
		t.Fatal("reloader must not run when creation was rejected")
	}
}

func TestAdminCreateSchema_AlreadyExists(t *testing.T) {
	tool := &adminCreateSchemaTool{
		creator:  &stubCreator{repo: &stubSchemaRepo{}, err: pkgerrors.AlreadyExists(`schema with name "dup" already exists`)},
		reloader: func(context.Context) {},
	}

	out, err := tool.InvokableRun(context.Background(), `{"name":"dup"}`)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.Contains(out, `Schema with name "dup" already exists`) {
		t.Fatalf("expected already-exists render, got: %s", out)
	}
}

func TestAdminCreateSchema_RequiresName(t *testing.T) {
	tool := &adminCreateSchemaTool{creator: &stubCreator{repo: &stubSchemaRepo{}}, reloader: func(context.Context) {}}
	out, _ := tool.InvokableRun(context.Background(), `{"description":"no name"}`)
	if !strings.Contains(out, "name is required") {
		t.Fatalf("expected name-required error, got: %s", out)
	}
}
