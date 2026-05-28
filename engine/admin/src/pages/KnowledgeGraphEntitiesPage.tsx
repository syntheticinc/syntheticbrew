import { useParams } from 'react-router-dom';
import { useState, useEffect, useMemo } from 'react';
import { api } from '../api/client';
import PageContainer from '../components/PageContainer';
import type { KGEntity, KGEntitySchema, KGEntitiesListResponse } from '../types';

/**
 * Paginated, filterable browser for entities under one entity_type. Filters
 * are auto-generated from the schema's x-index properties. Cross-refs in
 * inspected entities are rendered as clickable links pointing at the
 * referenced entity's detail page.
 */
export default function KnowledgeGraphEntitiesPage() {
  const { bundle: bundleName = '', entityType = '' } = useParams<{
    bundle: string;
    entityType: string;
  }>();
  const [schema, setSchema] = useState<KGEntitySchema | null>(null);
  const [list, setList] = useState<KGEntitiesListResponse | null>(null);
  const [filters, setFilters] = useState<Record<string, string>>({});
  const [offset, setOffset] = useState(0);
  const [inspected, setInspected] = useState<KGEntity | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const limit = 50;

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    api
      .getKGSchema(bundleName, entityType)
      .then((s) => {
        if (!cancelled) setSchema(s);
      })
      .catch((e) => {
        if (!cancelled) setError(String(e));
      });
    return () => {
      cancelled = true;
    };
  }, [bundleName, entityType]);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    api
      .listKGEntities(bundleName, entityType, filters, limit, offset)
      .then((r) => {
        if (!cancelled) {
          setList(r);
          setLoading(false);
        }
      })
      .catch((e) => {
        if (!cancelled) {
          setError(String(e));
          setLoading(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [bundleName, entityType, filters, offset]);

  const indexedProperties = useMemo(() => {
    if (!schema) return [];
    const schemaJson = (schema.schema_json as { properties?: Record<string, unknown> })?.properties;
    if (!schemaJson) return [];
    const result: Array<{ name: string; prop: Record<string, unknown> }> = [];
    for (const [name, prop] of Object.entries(schemaJson)) {
      if ((prop as Record<string, unknown>)['x-index'] === true) {
        result.push({ name, prop: prop as Record<string, unknown> });
      }
    }
    return result;
  }, [schema]);

  if (error) {
    return (
      <PageContainer>
        <div className="p-8 text-red-400 text-sm">{error}</div>
      </PageContainer>
    );
  }

  return (
    <PageContainer wide>
      <header className="border-b border-brand-shade3/20 px-4 py-3">
        <h1 className="text-lg font-semibold text-brand-light">{entityType}</h1>
        <p className="text-xs text-brand-shade3">Entities in bundle "{bundleName}"</p>
      </header>
      <div className="flex h-full">
        {/* Filter panel */}
        <aside className="w-64 border-r border-brand-shade3/20 p-4 space-y-3">
          <h3 className="text-xs uppercase tracking-widest text-brand-shade3 mb-2">
            Filters
          </h3>
          {indexedProperties.length === 0 && (
            <p className="text-xs text-brand-shade3">No filterable fields.</p>
          )}
          {indexedProperties.map(({ name, prop }) => (
            <FilterField
              key={name}
              name={name}
              prop={prop}
              value={filters[name] ?? ''}
              onChange={(v) => {
                const next = { ...filters };
                if (v) next[name] = v;
                else delete next[name];
                setFilters(next);
                setOffset(0);
              }}
            />
          ))}
        </aside>

        {/* Entity list */}
        <main className="flex-1 p-4 overflow-auto">
          {loading && <div className="text-brand-shade3 text-sm">Loading…</div>}
          {!loading && list && (
            <>
              <div className="mb-3 flex items-center justify-between text-xs text-brand-shade3">
                <span>
                  Showing {list.items.length} of {list.total} entities
                </span>
                <div className="flex gap-2">
                  <button
                    onClick={() => setOffset(Math.max(0, offset - limit))}
                    disabled={offset === 0}
                    className="px-2 py-1 border border-brand-shade3/20 rounded text-xs disabled:opacity-30"
                  >
                    ← Prev
                  </button>
                  <button
                    onClick={() => setOffset(offset + limit)}
                    disabled={offset + limit >= list.total}
                    className="px-2 py-1 border border-brand-shade3/20 rounded text-xs disabled:opacity-30"
                  >
                    Next →
                  </button>
                </div>
              </div>
              <table className="w-full text-sm">
                <thead className="bg-brand-dark-alt">
                  <tr>
                    <th className="text-left p-2 font-medium text-brand-light text-xs">ID</th>
                    <th className="text-left p-2 font-medium text-brand-light text-xs">Preview</th>
                    <th className="text-right p-2 font-medium text-brand-light text-xs">Inspect</th>
                  </tr>
                </thead>
                <tbody>
                  {list.items.map((e) => (
                    <tr key={e.entity_id} className="border-t border-brand-shade3/10 hover:bg-brand-dark-alt/30">
                      <td className="p-2 font-mono text-xs text-brand-light">{e.entity_id}</td>
                      <td className="p-2 text-xs text-brand-shade2 max-w-md truncate">
                        {previewEntity(e.data)}
                      </td>
                      <td className="p-2 text-right">
                        <button
                          onClick={() => setInspected(e)}
                          className="text-xs text-brand-accent hover:underline"
                        >
                          Inspect
                        </button>
                      </td>
                    </tr>
                  ))}
                  {list.items.length === 0 && (
                    <tr>
                      <td colSpan={3} className="p-8 text-center text-brand-shade3 text-xs">
                        No entities match the current filters.
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            </>
          )}
        </main>

        {/* Inspect drawer */}
        {inspected && (
          <aside className="w-[420px] border-l border-brand-shade3/20 p-4 overflow-auto">
            <div className="flex items-center justify-between mb-3">
              <h3 className="text-sm font-semibold text-brand-light">Entity {inspected.entity_id}</h3>
              <button onClick={() => setInspected(null)} className="text-brand-shade3 hover:text-brand-light text-lg">
                ×
              </button>
            </div>
            <pre className="p-3 bg-brand-dark-alt rounded-card border border-brand-shade3/20 text-xs text-brand-shade2 overflow-auto max-h-[calc(100vh-200px)]">
              {JSON.stringify(inspected.data, null, 2)}
            </pre>
          </aside>
        )}
      </div>
    </PageContainer>
  );
}

function FilterField({
  name,
  prop,
  value,
  onChange,
}: {
  name: string;
  prop: Record<string, unknown>;
  value: string;
  onChange: (v: string) => void;
}) {
  const enumValues = Array.isArray(prop.enum) ? (prop.enum as string[]) : null;
  if (enumValues) {
    return (
      <div>
        <label className="block text-xs text-brand-shade2 mb-1">{name}</label>
        <select
          value={value}
          onChange={(e) => onChange(e.target.value)}
          className="w-full bg-brand-dark-alt border border-brand-shade3/20 rounded px-2 py-1 text-xs text-brand-light"
        >
          <option value="">All</option>
          {enumValues.map((v) => (
            <option key={v} value={v}>
              {v}
            </option>
          ))}
        </select>
      </div>
    );
  }
  return (
    <div>
      <label className="block text-xs text-brand-shade2 mb-1">{name}</label>
      <input
        type="text"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={`Filter by ${name}…`}
        className="w-full bg-brand-dark-alt border border-brand-shade3/20 rounded px-2 py-1 text-xs text-brand-light"
      />
    </div>
  );
}

function previewEntity(data: Record<string, unknown>): string {
  const keys = Object.keys(data).slice(0, 4);
  return keys.map((k) => `${k}: ${JSON.stringify(data[k])}`).join(' · ');
}
