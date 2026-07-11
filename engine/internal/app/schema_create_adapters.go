package app

import (
	"context"
	"fmt"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
	admintools "github.com/syntheticinc/syntheticbrew/internal/infrastructure/tools/admin"
	"github.com/syntheticinc/syntheticbrew/internal/usecase/schemacreate"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// schemaCreateRepoAdapter implements schemacreate.SchemaRepository over the
// GORM schema repository. It owns the persistence-boundary concern of
// translating driver duplicate-key failures into a typed DomainError so the
// usecase (and every facade above it) never string-matches driver messages.
type schemaCreateRepoAdapter struct {
	repo *configrepo.GORMSchemaRepository
}

func (a *schemaCreateRepoAdapter) Create(ctx context.Context, record *schemacreate.SchemaRecord) error {
	rec := &configrepo.SchemaRecord{
		Name:         record.Name,
		Description:  record.Description,
		EntryAgentID: record.EntryAgentID,
		ChatEnabled:  record.ChatEnabled,
	}
	if err := a.repo.Create(ctx, rec); err != nil {
		if isDuplicateKeyErr(err) {
			return pkgerrors.AlreadyExists(fmt.Sprintf("schema with name %q already exists", record.Name))
		}
		return err
	}
	record.ID = rec.ID
	record.CreatedAt = rec.CreatedAt
	return nil
}

// newSchemaCreateUsecase assembles the guarded schema-creation usecase — the
// single business-logic seam every user-facing creation facade routes
// through. guard is the runtime plugin (Noop in CE admits everything).
func newSchemaCreateUsecase(repo *configrepo.GORMSchemaRepository, guard schemacreate.CreateGuard) *schemacreate.Usecase {
	return schemacreate.New(&schemaCreateRepoAdapter{repo: repo}, guard)
}

// adminSchemaCreatorAdapter exposes the schema-creation usecase to the admin
// tools under their consumer-side SchemaCreator contract.
type adminSchemaCreatorAdapter struct {
	uc *schemacreate.Usecase
}

func newAdminSchemaCreatorAdapter(uc *schemacreate.Usecase) admintools.SchemaCreator {
	return &adminSchemaCreatorAdapter{uc: uc}
}

func (a *adminSchemaCreatorAdapter) CreateSchema(ctx context.Context, name, description string) (*admintools.SchemaRecord, error) {
	out, err := a.uc.Execute(ctx, schemacreate.Input{Name: name, Description: description})
	if err != nil {
		return nil, err
	}
	return &admintools.SchemaRecord{
		ID:          out.ID,
		Name:        out.Name,
		Description: out.Description,
	}, nil
}
