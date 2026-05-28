import { useNavigate } from 'react-router-dom';
import { api } from '../api/client';
import { useApi } from '../hooks/useApi';
import { useAdminRefresh } from '../hooks/useAdminRefresh';
import DataTable from '../components/DataTable';
import PageContainer from '../components/PageContainer';
import type { KGBundle } from '../types';

// Empty-state icon: taxonomy hierarchy — parent entity over two children.
// Consistent with Sidebar.knowledgeGraphs + CapabilityBlock 'graph' icon.
const graphIcon = (
  <svg
    className="w-12 h-12 text-brand-shade3/40"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth="1.2"
    strokeLinecap="round"
    strokeLinejoin="round"
  >
    <rect x="9" y="2.5" width="6" height="5" rx="1" />
    <rect x="2.5" y="14" width="6" height="5" rx="1" />
    <rect x="15.5" y="14" width="6" height="5" rx="1" />
    <path d="M12 7.5 V11" />
    <path d="M12 11 H5.5 V14" />
    <path d="M12 11 H18.5 V14" />
  </svg>
);

function entityCount(bundle: KGBundle): number {
  const types = bundle.manifest?.entity_types ?? [];
  return types.length;
}

function totalEntities(bundle: KGBundle): number {
  const counts = bundle.manifest?.counts ?? {};
  let total = 0;
  for (const v of Object.values(counts)) {
    if (typeof v === 'number') total += v;
  }
  return total;
}

export default function KnowledgeGraphsPage() {
  const navigate = useNavigate();
  const { data: bundles, loading, error, refetch } = useApi(() => api.listKnowledgeGraphs());
  useAdminRefresh(refetch);

  function handleRowClick(b: KGBundle) {
    navigate(`/knowledge-graphs/${encodeURIComponent(b.bundle_name)}`);
  }

  const columns = [
    {
      key: 'bundle_name',
      header: 'Bundle',
      render: (row: KGBundle) => (
        <span className="text-[13px] text-brand-light font-medium">{row.bundle_name}</span>
      ),
    },
    {
      key: 'version',
      header: 'Version',
      render: (row: KGBundle) => (
        <span className="text-[11px] text-brand-shade3 font-mono">{row.version || '--'}</span>
      ),
    },
    {
      key: 'entity_types',
      header: 'Entity Types',
      render: (row: KGBundle) => (
        <span className="text-[13px] text-brand-shade3">{entityCount(row)}</span>
      ),
    },
    {
      key: 'total_entities',
      header: 'Entities',
      render: (row: KGBundle) => (
        <span className="text-[13px] text-brand-shade3">{totalEntities(row)}</span>
      ),
    },
    {
      key: 'updated_at',
      header: 'Updated',
      render: (row: KGBundle) => (
        <span className="text-[11px] text-brand-shade3 font-mono">
          {row.updated_at ? new Date(row.updated_at).toLocaleString() : '--'}
        </span>
      ),
    },
  ];

  if (loading) return <div className="text-brand-shade3">Loading knowledge graphs...</div>;
  if (error) return <div className="text-red-400">Error: {error}</div>;

  const rows = bundles ?? [];

  return (
    <PageContainer>
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-2xl font-bold text-brand-light">Knowledge Graphs</h1>
          <p className="text-xs text-brand-shade3 mt-1">
            Structured taxonomy bundles. Each bundle exposes entity types as tools agents can call.
          </p>
        </div>
      </div>

      {rows.length === 0 ? (
        // Empty state: rich onboarding with brewctl + admin guide. Renders
        // instead of the DataTable to avoid duplicating "no rows" messaging.
        <div className="rounded-card border border-brand-shade3/15 bg-brand-dark-alt p-5">
          <div className="flex items-start gap-4">
            <div className="shrink-0">{graphIcon}</div>
            <div className="min-w-0 flex-1">
              <p className="text-sm font-semibold text-brand-light mb-1">No knowledge graphs yet</p>
              <p className="text-xs text-brand-shade3 mb-3 max-w-xl">
                Declare entity schemas + instances; the engine auto-generates{' '}
                <code className="px-1 py-0.5 bg-brand-dark rounded text-brand-shade2 font-mono text-[11px]">list_X</code>{' '}
                /{' '}
                <code className="px-1 py-0.5 bg-brand-dark rounded text-brand-shade2 font-mono text-[11px]">get_X</code>{' '}
                MCP tools for bound agents. Apply via brewctl or POST to{' '}
                <code className="px-1 py-0.5 bg-brand-dark rounded text-brand-shade2 font-mono text-[11px]">
                  /api/v1/knowledge-graphs/&#123;bundle&#125;/import
                </code>
                .
              </p>
              <pre className="text-[11px] font-mono text-brand-shade2 bg-brand-dark border border-brand-shade3/10 rounded-btn p-3 overflow-x-auto">
{`# my-bundle/manifest.yaml + schemas/ + entities/
bundle_name: my-catalog
version: 1.0.0
entity_types:
  - name: category
    schema_file: schemas/category.schema.json
    entities_file: entities/categories.yaml

$ brewctl kg apply ./my-bundle`}
              </pre>
              <a
                href="https://syntheticbrew.ai/docs/concepts/knowledge-graphs/"
                target="_blank"
                rel="noreferrer"
                className="inline-flex items-center gap-1 mt-3 text-xs text-brand-accent hover:underline"
              >
                Read the docs
                <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                  <path d="M18 13v6a2 2 0 01-2 2H5a2 2 0 01-2-2V8a2 2 0 012-2h6" />
                  <polyline points="15 3 21 3 21 9" />
                  <line x1="10" y1="14" x2="21" y2="3" />
                </svg>
              </a>
            </div>
          </div>
        </div>
      ) : (
        <div className="bg-brand-dark-alt rounded-card border border-brand-shade3/15">
          <DataTable
            columns={columns}
            data={rows}
            keyField="bundle_name"
            onRowClick={handleRowClick}
            emptyMessage="No knowledge graphs configured"
            emptyIcon={graphIcon}
          />
        </div>
      )}
    </PageContainer>
  );
}
