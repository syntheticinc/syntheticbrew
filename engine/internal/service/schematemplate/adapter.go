package schematemplate

import (
	"context"

	ucschematemplate "github.com/syntheticinc/syntheticbrew/internal/usecase/schematemplate"
)

// UsecaseForkerAdapter bridges the service-level ForkService into the
// usecase-level Forker interface, translating the concrete ForkedSchema
// result into the usecase-visible ForkResult shape. Kept in a separate
// file to keep `fork.go` free of usecase imports — the service package
// remains the downstream dependency.
type UsecaseForkerAdapter struct {
	svc *ForkService
}

// NewUsecaseForkerAdapter wraps a ForkService into the usecase Forker
// contract.
func NewUsecaseForkerAdapter(svc *ForkService) *UsecaseForkerAdapter {
	return &UsecaseForkerAdapter{svc: svc}
}

// Fork implements usecase/schematemplate.Forker.
func (a *UsecaseForkerAdapter) Fork(ctx context.Context, templateName, newSchemaName string) (*ucschematemplate.ForkResult, error) {
	forked, err := a.svc.Fork(ctx, templateName, newSchemaName)
	if err != nil {
		return nil, err
	}
	return &ucschematemplate.ForkResult{
		SchemaID:   forked.SchemaID,
		SchemaName: forked.SchemaName,
		AgentIDs:   forked.AgentIDs,
	}, nil
}
