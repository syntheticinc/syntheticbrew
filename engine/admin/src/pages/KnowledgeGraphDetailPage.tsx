import { useParams, useNavigate, Link } from 'react-router-dom';
import { useState } from 'react';
import { api } from '../api/client';
import { useApi } from '../hooks/useApi';
import PageContainer from '../components/PageContainer';
import type { KGBundle, KGEntitySchema } from '../types';
import { kgSummaryFields } from '../types';

/**
 * Detail view for one Knowledge Graph bundle. Shows the manifest summary,
 * lists all entity schemas in the bundle, and provides navigation links to
 * the per-entity-type browser. Read-only — mutations happen via brewctl.
 */
export default function KnowledgeGraphDetailPage() {
  const { bundle: bundleName = '' } = useParams<{ bundle: string }>();
  const navigate = useNavigate();
  const [manifestExpanded, setManifestExpanded] = useState(false);

  const { data: bundle, loading: bundleLoading } = useApi<KGBundle | null>(
    () => api.getKnowledgeGraph(bundleName),
    [bundleName],
  );
  const { data: schemas, loading: schemasLoading } = useApi<KGEntitySchema[]>(
    () => api.listKGSchemas(bundleName),
    [bundleName],
  );

  if (bundleLoading || schemasLoading) {
    return (
      <PageContainer>
        <div className="p-8 text-brand-shade3">Loading bundle…</div>
      </PageContainer>
    );
  }

  if (!bundle) {
    return (
      <PageContainer>
        <div className="p-8 text-brand-shade3">
          Bundle "{bundleName}" was not found.{' '}
          <button onClick={() => navigate('/knowledge-graphs')} className="text-brand-accent underline">
            Back to bundles
          </button>
        </div>
      </PageContainer>
    );
  }

  return (
    <PageContainer>
      <div className="space-y-6 p-6">
        <header className="border-b border-brand-shade3/20 pb-4">
          <h1 className="text-xl font-semibold text-brand-light">{bundle.bundle_name}</h1>
          <p className="text-xs text-brand-shade3 mt-1">
            Knowledge Graph bundle · version {bundle.version}
          </p>
        </header>
        <section>
          <div className="flex items-center justify-between mb-3">
            <h2 className="text-lg font-semibold text-brand-light">Entity Schemas</h2>
            <span className="text-xs text-brand-shade3">{(schemas ?? []).length} types</span>
          </div>
          <div className="border border-brand-shade3/20 rounded-card overflow-hidden">
            <table className="w-full text-sm">
              <thead className="bg-brand-dark-alt">
                <tr>
                  <th className="text-left p-3 font-medium text-brand-light">Entity type</th>
                  <th className="text-left p-3 font-medium text-brand-light">ID field</th>
                  <th className="text-left p-3 font-medium text-brand-light">Generated tools</th>
                  <th className="text-left p-3 font-medium text-brand-light">Summary fields</th>
                  <th className="text-right p-3 font-medium text-brand-light">Actions</th>
                </tr>
              </thead>
              <tbody>
                {(schemas ?? []).map((s) => {
                  const summary = kgSummaryFields(s);
                  return (
                    <tr key={s.entity_type} className="border-t border-brand-shade3/10">
                      <td className="p-3 text-brand-light font-mono text-xs">{s.entity_type}</td>
                      <td className="p-3 text-brand-shade2 font-mono text-xs">{s.id_field}</td>
                      <td className="p-3 text-brand-shade2 text-xs">
                        {(s.expose_tools ?? [])
                          .map((t) => (t === 'list_ids' ? `list_${s.entity_type}_ids` : `${t}_${s.entity_type}`))
                          .join(', ')}
                      </td>
                      <td className="p-3 text-brand-shade2 font-mono text-xs">
                        {summary.length === 0 ? (
                          <span className="text-brand-shade3">— (bare ids)</span>
                        ) : (
                          summary.join(', ')
                        )}
                      </td>
                      <td className="p-3 text-right">
                        <Link
                          to={`/knowledge-graphs/${bundleName}/entities/${s.entity_type}`}
                          className="text-xs text-brand-accent hover:underline"
                        >
                          Browse entities →
                        </Link>
                      </td>
                    </tr>
                  );
                })}
                {(schemas ?? []).length === 0 && (
                  <tr>
                    <td colSpan={5} className="p-6 text-center text-brand-shade3 text-xs">
                      No schemas — apply a bundle via{' '}
                      <code className="bg-brand-dark-alt px-1 py-0.5 rounded">brewctl kg apply</code>
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </section>

        <section>
          <button
            onClick={() => setManifestExpanded((v) => !v)}
            className="text-sm font-semibold text-brand-light flex items-center gap-2 mb-2"
          >
            <span>{manifestExpanded ? '▾' : '▸'}</span> Manifest JSON
          </button>
          {manifestExpanded && (
            <pre className="p-4 bg-brand-dark-alt rounded-card border border-brand-shade3/20 text-xs text-brand-shade2 overflow-auto max-h-96">
              {JSON.stringify(bundle.manifest, null, 2)}
            </pre>
          )}
        </section>
      </div>
    </PageContainer>
  );
}
