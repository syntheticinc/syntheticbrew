package http

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"
)

// fakeSchemaNameResolver implements SchemaNameResolver for testing without
// hitting a real DB. Closure makes per-test wiring trivial.
type fakeSchemaNameResolver struct {
	fn func(ctx context.Context, name string) (string, error)
}

func (f *fakeSchemaNameResolver) GetSchemaIDByName(ctx context.Context, name string) (string, error) {
	return f.fn(ctx, name)
}

type fakeKBNameResolver struct {
	fn func(ctx context.Context, name string) (string, error)
}

func (f *fakeKBNameResolver) GetKBIDByName(ctx context.Context, name string) (string, error) {
	return f.fn(ctx, name)
}

func TestResolveSchemaNameToUUID_HappyPath(t *testing.T) {
	repo := &fakeSchemaNameResolver{
		fn: func(_ context.Context, name string) (string, error) {
			if name == "support" {
				return "550e8400-e29b-41d4-a716-446655440000", nil
			}
			return "", gorm.ErrRecordNotFound
		},
	}
	id, err := resolveSchemaNameToUUID(context.Background(), repo, "support")
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if id != "550e8400-e29b-41d4-a716-446655440000" {
		t.Fatalf("unexpected id: %s", id)
	}
}

func TestResolveSchemaNameToUUID_InvalidName(t *testing.T) {
	repoNotCalled := &fakeSchemaNameResolver{
		fn: func(_ context.Context, _ string) (string, error) {
			t.Fatal("repo should not be called when validation fails")
			return "", nil
		},
	}
	cases := []struct {
		name    string
		input   string
		wantErr error
	}{
		{"empty", "", ErrNameEmpty},
		{"slash", "foo/bar", ErrInvalidName},
		{"uppercase", "Foo", ErrInvalidName},
		{"reserved chat", "chat", ErrReservedName},
		{"uuid-shaped", "550e8400-e29b-41d4-a716-446655440000", ErrUUIDShapedName},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolveSchemaNameToUUID(context.Background(), repoNotCalled, tc.input)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestResolveSchemaNameToUUID_NotFound(t *testing.T) {
	repo := &fakeSchemaNameResolver{
		fn: func(_ context.Context, _ string) (string, error) {
			return "", gorm.ErrRecordNotFound
		},
	}
	_, err := resolveSchemaNameToUUID(context.Background(), repo, "missing")
	if !errors.Is(err, ErrRefNotFound) {
		t.Fatalf("expected ErrRefNotFound, got %v", err)
	}
}

func TestResolveSchemaNameToUUID_RepoError(t *testing.T) {
	repoErr := errors.New("connection lost")
	repo := &fakeSchemaNameResolver{
		fn: func(_ context.Context, _ string) (string, error) {
			return "", repoErr
		},
	}
	_, err := resolveSchemaNameToUUID(context.Background(), repo, "support")
	if errors.Is(err, ErrRefNotFound) {
		t.Fatalf("repo error must NOT be coerced to ErrRefNotFound")
	}
	if !errors.Is(err, repoErr) {
		t.Fatalf("expected wrapped repo error, got %v", err)
	}
}

func TestResolveKBNameToUUID_HappyPath(t *testing.T) {
	repo := &fakeKBNameResolver{
		fn: func(_ context.Context, name string) (string, error) {
			if name == "handbook" {
				return "11111111-2222-3333-4444-555555555555", nil
			}
			return "", gorm.ErrRecordNotFound
		},
	}
	id, err := resolveKBNameToUUID(context.Background(), repo, "handbook")
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if id != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("unexpected id: %s", id)
	}
}

func TestResolveKBNameToUUID_NotFound(t *testing.T) {
	repo := &fakeKBNameResolver{
		fn: func(_ context.Context, _ string) (string, error) {
			return "", gorm.ErrRecordNotFound
		},
	}
	_, err := resolveKBNameToUUID(context.Background(), repo, "missing-kb")
	if !errors.Is(err, ErrRefNotFound) {
		t.Fatalf("expected ErrRefNotFound, got %v", err)
	}
}

func TestResolveKBNameToUUID_InvalidName(t *testing.T) {
	repoNotCalled := &fakeKBNameResolver{
		fn: func(_ context.Context, _ string) (string, error) {
			t.Fatal("repo should not be called when validation fails")
			return "", nil
		},
	}
	_, err := resolveKBNameToUUID(context.Background(), repoNotCalled, "Bad/KB")
	if !errors.Is(err, ErrInvalidName) {
		t.Fatalf("expected ErrInvalidName, got %v", err)
	}
}
