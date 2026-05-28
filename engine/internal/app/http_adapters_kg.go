package app

import (
	"encoding/json"
	"net/http"

	deliveryhttp "github.com/syntheticinc/syntheticbrew/internal/delivery/http"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/usecase/kgapply"
	"github.com/syntheticinc/syntheticbrew/internal/usecase/kgmutate"
	"github.com/syntheticinc/syntheticbrew/internal/usecase/kgread"
)

// kgReadHTTPAdapter wraps the kgread.Usecase into the consumer-side
// KGReadService interface expected by deliveryhttp.KGReadHandler. The
// adapter handles HTTP-domain translation (domain entities → HTTP DTOs) so
// the handler stays bs-free and the usecase stays HTTP-free.
type kgReadHTTPAdapter struct {
	uc *kgread.Usecase
}

func newKGReadHTTPAdapter(uc *kgread.Usecase) *kgReadHTTPAdapter {
	return &kgReadHTTPAdapter{uc: uc}
}

func (a *kgReadHTTPAdapter) ListBundles(r *http.Request) ([]deliveryhttp.KGBundleInfo, error) {
	tenantID := tenantIDOrCE(r)
	bundles, err := a.uc.ListBundles(r.Context(), tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]deliveryhttp.KGBundleInfo, 0, len(bundles))
	for _, b := range bundles {
		out = append(out, kgBundleToInfo(b))
	}
	return out, nil
}

func (a *kgReadHTTPAdapter) GetBundle(r *http.Request, bundleName string) (*deliveryhttp.KGBundleInfo, error) {
	tenantID := tenantIDOrCE(r)
	b, err := a.uc.GetBundle(r.Context(), tenantID, bundleName)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, nil
	}
	info := kgBundleToInfo(b)
	return &info, nil
}

func (a *kgReadHTTPAdapter) ListSchemas(r *http.Request, bundleName string) ([]deliveryhttp.KGSchemaInfo, error) {
	tenantID := tenantIDOrCE(r)
	schemas, err := a.uc.ListSchemas(r.Context(), tenantID, bundleName)
	if err != nil {
		return nil, err
	}
	out := make([]deliveryhttp.KGSchemaInfo, 0, len(schemas))
	for _, s := range schemas {
		out = append(out, kgSchemaToInfo(s))
	}
	return out, nil
}

func (a *kgReadHTTPAdapter) GetSchema(r *http.Request, bundleName, entityType string) (*deliveryhttp.KGSchemaInfo, error) {
	tenantID := tenantIDOrCE(r)
	s, err := a.uc.GetSchema(r.Context(), tenantID, bundleName, entityType)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, nil
	}
	info := kgSchemaToInfo(s)
	return &info, nil
}

func (a *kgReadHTTPAdapter) ListEntities(
	r *http.Request,
	bundleName, entityType string,
	filters map[string]any,
	limit, offset int,
) (*deliveryhttp.KGEntitiesListResponse, error) {
	tenantID := tenantIDOrCE(r)
	items, total, err := a.uc.ListEntities(r.Context(), kgread.ListEntitiesQuery{
		TenantID:   tenantID,
		BundleName: bundleName,
		EntityType: entityType,
		Filters:    filters,
		Limit:      limit,
		Offset:     offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]deliveryhttp.KGEntityInfo, 0, len(items))
	for _, e := range items {
		out = append(out, kgEntityToInfo(e))
	}
	return &deliveryhttp.KGEntitiesListResponse{
		Items:  out,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	}, nil
}

func (a *kgReadHTTPAdapter) GetEntity(r *http.Request, bundleName, entityType, entityID string) (*deliveryhttp.KGEntityInfo, error) {
	tenantID := tenantIDOrCE(r)
	e, err := a.uc.GetEntity(r.Context(), tenantID, bundleName, entityType, entityID)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, nil
	}
	info := kgEntityToInfo(e)
	return &info, nil
}

// kgMutateHTTPAdapter wraps the kgapply + kgmutate usecases.
type kgMutateHTTPAdapter struct {
	applyUC  *kgapply.Usecase
	mutateUC *kgmutate.Usecase
}

func newKGMutateHTTPAdapter(applyUC *kgapply.Usecase, mutateUC *kgmutate.Usecase) *kgMutateHTTPAdapter {
	return &kgMutateHTTPAdapter{applyUC: applyUC, mutateUC: mutateUC}
}

func (a *kgMutateHTTPAdapter) BulkImport(r *http.Request, bundleName string, req deliveryhttp.BulkImportRequest) error {
	tenantID := tenantIDOrCE(r)

	schemas := make([]kgapply.SchemaInput, 0, len(req.Schemas))
	for _, s := range req.Schemas {
		schemas = append(schemas, kgapply.SchemaInput{
			EntityType:      s.EntityType,
			SchemaJSON:      []byte(s.Schema),
			ExposeTools:     s.ExposeTools,
			ToolDescription: s.ToolDescription,
		})
	}
	entitySets := make([]kgapply.EntitySetInput, 0, len(req.Entities))
	for _, g := range req.Entities {
		entitySets = append(entitySets, kgapply.EntitySetInput{
			EntityType: g.EntityType,
			Items:      g.Items,
		})
	}

	_, err := a.applyUC.Execute(r.Context(), kgapply.Input{
		TenantID:   tenantID,
		BundleName: bundleName,
		Version:    req.Version,
		Schemas:    schemas,
		Entities:   entitySets,
	})
	return err
}

func (a *kgMutateHTTPAdapter) CreateEntity(r *http.Request, bundleName, entityType string, data map[string]any) (*deliveryhttp.KGEntityInfo, error) {
	tenantID := tenantIDOrCE(r)
	e, err := a.mutateUC.CreateEntity(r.Context(), kgmutate.CreateEntityInput{
		TenantID:   tenantID,
		BundleName: bundleName,
		EntityType: entityType,
		Data:       data,
	})
	if err != nil {
		return nil, err
	}
	info := kgEntityToInfo(e)
	return &info, nil
}

func (a *kgMutateHTTPAdapter) UpdateEntity(r *http.Request, bundleName, entityType, entityID string, data map[string]any) (*deliveryhttp.KGEntityInfo, error) {
	tenantID := tenantIDOrCE(r)
	e, err := a.mutateUC.UpdateEntity(r.Context(), kgmutate.UpdateEntityInput{
		TenantID:   tenantID,
		BundleName: bundleName,
		EntityType: entityType,
		EntityID:   entityID,
		Data:       data,
	})
	if err != nil {
		return nil, err
	}
	info := kgEntityToInfo(e)
	return &info, nil
}

func (a *kgMutateHTTPAdapter) DeleteEntity(r *http.Request, bundleName, entityType, entityID string) error {
	tenantID := tenantIDOrCE(r)
	return a.mutateUC.DeleteEntity(r.Context(), tenantID, bundleName, entityType, entityID)
}

func (a *kgMutateHTTPAdapter) UpsertSchema(r *http.Request, bundleName, entityType string, req deliveryhttp.UpsertSchemaRequest) (*deliveryhttp.KGSchemaInfo, error) {
	tenantID := tenantIDOrCE(r)
	s, err := a.mutateUC.UpsertSchema(r.Context(), kgmutate.UpsertSchemaInput{
		TenantID:        tenantID,
		BundleName:      bundleName,
		EntityType:      entityType,
		SchemaJSON:      []byte(req.Schema),
		ExposeTools:     req.ExposeTools,
		ToolDescription: req.ToolDescription,
	})
	if err != nil {
		return nil, err
	}
	info := kgSchemaToInfo(s)
	return &info, nil
}

func (a *kgMutateHTTPAdapter) DeleteBundle(r *http.Request, bundleName string) error {
	tenantID := tenantIDOrCE(r)
	return a.mutateUC.DeleteBundle(r.Context(), tenantID, bundleName)
}

// --- helpers ---

// tenantIDOrCE returns the tenant_id from the request context, or
// domain.CETenantID as a fallback. CE/single-tenant deployments do not
// inject tenant middleware, so without this fallback every KG usecase
// would reject with "tenant_id required".
func tenantIDOrCE(r *http.Request) string {
	if tid := domain.TenantIDFromContext(r.Context()); tid != "" {
		return tid
	}
	return domain.CETenantID
}

func kgBundleToInfo(b *domain.KGBundle) deliveryhttp.KGBundleInfo {
	return deliveryhttp.KGBundleInfo{
		BundleName: b.BundleName,
		Version:    b.Version,
		Manifest:   b.Manifest,
		CreatedAt:  b.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:  b.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

func kgSchemaToInfo(s *domain.KGEntitySchema) deliveryhttp.KGSchemaInfo {
	return deliveryhttp.KGSchemaInfo{
		BundleName:      s.BundleName,
		EntityType:      s.EntityType,
		SchemaJSON:      json.RawMessage(s.SchemaJSON),
		SchemaHash:      s.SchemaHash,
		IDField:         s.IDField,
		ExposeTools:     s.ExposeTools,
		ToolDescription: s.ToolDescription,
	}
}

func kgEntityToInfo(e *domain.KGEntity) deliveryhttp.KGEntityInfo {
	return deliveryhttp.KGEntityInfo{
		BundleName: e.BundleName,
		EntityType: e.EntityType,
		EntityID:   e.EntityID,
		Data:       json.RawMessage(e.Data),
		SchemaHash: e.SchemaHash,
		CreatedAt:  e.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:  e.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}
