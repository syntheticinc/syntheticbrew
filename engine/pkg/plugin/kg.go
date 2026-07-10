package plugin

import (
	"context"
	"errors"
)

// ErrKGQuotaExceeded is returned by KGEnforcer.OnEntityWrite when the tenant's
// Knowledge Graph quota is exhausted. The engine surfaces it as HTTP 402 to
// the calling client; a plugin implementation may also record a usage event
// before returning.
var ErrKGQuotaExceeded = errors.New("knowledge graphs quota exceeded")

// KGEnforcer gates writes to the Knowledge Graphs subsystem at the usecase
// boundary. It is invoked from every mutation path — bulk apply (kgapply),
// granular create / update / delete (kgmutate) — so quota cannot be bypassed
// by choosing one path over another.
//
// The base engine returns nil from Plugin.KGEnforcer to disable enforcement;
// a plugin returns a real implementation that checks the tenant's configured
// limits and records a usage event.
//
// deltaEntities is the change in entity count (positive for add, negative
// for delete, zero for replace). deltaBytes is the change in stored JSONB
// size in bytes.
type KGEnforcer interface {
	OnEntityWrite(ctx context.Context, tenantID, bundleName string, deltaEntities int, deltaBytes int64) error
}

// KGCounter exposes per-tenant Knowledge Graph counters for the admin UI and
// usage displays. The base engine returns nil from Plugin.KGCounter; it
// then reads counts directly from the database without extra enrichment.
type KGCounter interface {
	BundlesCount(ctx context.Context, tenantID string) (int, error)
	EntitiesCount(ctx context.Context, tenantID string) (int, error)
}
