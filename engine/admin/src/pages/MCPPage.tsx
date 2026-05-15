import { useState, useMemo, type FormEvent } from 'react';
import { useNavigate } from 'react-router-dom';
import { api } from '../api/client';
import { useApi } from '../hooks/useApi';
import DataTable from '../components/DataTable';
import { emptyIcons } from '../components/EmptyState';
import StatusBadge from '../components/StatusBadge';
import DetailPanel, { DetailRow, DetailSection } from '../components/DetailPanel';
import FormModal from '../components/FormModal';
import FormField from '../components/FormField';
import ConfirmDialog from '../components/ConfirmDialog';
import Modal from '../components/Modal';
import PageContainer from '../components/PageContainer';
import { ToastProvider, useToast } from '../components/builder/Toast';
import type { MCPServer, MCPCatalogEntry, MCPCatalogPackage, CreateMCPServerRequest, MCPCatalogCategory, CircuitBreakerState } from '../types';

// ─── Category meta ──────────────────────────────────────────────────────────

const CATEGORY_META: Record<MCPCatalogCategory | 'all', { label: string; color: string }> = {
  all:            { label: 'All',            color: 'bg-brand-shade3/15 text-brand-shade2' },
  search:         { label: 'Search',         color: 'bg-blue-500/15 text-blue-400' },
  data:           { label: 'Data',           color: 'bg-emerald-500/15 text-emerald-400' },
  communication:  { label: 'Communication',  color: 'bg-purple-500/15 text-purple-400' },
  'dev-tools':    { label: 'Dev Tools',      color: 'bg-amber-500/15 text-amber-400' },
  productivity:   { label: 'Productivity',   color: 'bg-pink-500/15 text-pink-400' },
  payments:       { label: 'Payments',       color: 'bg-orange-500/15 text-orange-400' },
  generic:        { label: 'Generic',        color: 'bg-brand-shade3/15 text-brand-shade2' },
};

const emptyForm: CreateMCPServerRequest = {
  name: '',
  type: 'stdio',
  command: '',
  args: [],
  url: '',
};

export default function MCPPage() {
  return (
    <ToastProvider>
      <MCPPageInner />
    </ToastProvider>
  );
}

function MCPPageInner() {
  const navigate = useNavigate();
  const { addToast } = useToast();
  const { data: servers, loading, error, refetch } = useApi(() => api.listMCPServers());
  const { data: catalog } = useApi(() => api.listCatalog());
  const { data: circuitBreakers, refetch: refetchCircuitBreakers } = useApi(() => api.listCircuitBreakers());
  const [resetting, setResetting] = useState(false);

  const [selected, setSelected] = useState<MCPServer | null>(null);
  const [showForm, setShowForm] = useState(false);
  const [showCatalog, setShowCatalog] = useState(false);
  const [editTarget, setEditTarget] = useState<MCPServer | null>(null);
  const [customForm, setCustomForm] = useState<CreateMCPServerRequest>({ ...emptyForm });
  const [envInput, setEnvInput] = useState<Record<string, string>>({});
  const [argsInput, setArgsInput] = useState('');
  const [saving, setSaving] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);
  const [authType, setAuthType] = useState<string>('none');
  const [authEnvVar, setAuthEnvVar] = useState('');
  const [authClientId, setAuthClientId] = useState('');
  const [forwardHeadersInput, setForwardHeadersInput] = useState('');
  // Empty string = "auto-refresh disabled"; otherwise the typed integer that
  // the form serialises into a number on submit (validated server-side too).
  const [refreshIntervalInput, setRefreshIntervalInput] = useState<string>('');
  const [refreshing, setRefreshing] = useState<string | null>(null);

  // Catalog search/filter state
  const [catalogSearch, setCatalogSearch] = useState('');
  const [catalogCategory, setCatalogCategory] = useState<MCPCatalogCategory | 'all'>('all');
  const [catalogDetail, setCatalogDetail] = useState<MCPCatalogEntry | null>(null);
  const [selectedPkgIdx, setSelectedPkgIdx] = useState(0);

  const circuitStateMap = useMemo(() => {
    const map: Record<string, CircuitBreakerState> = {};
    if (circuitBreakers) {
      for (const cb of circuitBreakers) {
        map[cb.name] = cb;
      }
    }
    return map;
  }, [circuitBreakers]);

  const filteredCatalog = useMemo(() => {
    if (!catalog) return [];
    return catalog.filter((entry) => {
      if (catalogCategory !== 'all' && entry.category !== catalogCategory) return false;
      if (catalogSearch) {
        const q = catalogSearch.toLowerCase();
        return entry.display.toLowerCase().includes(q) || entry.name.toLowerCase().includes(q);
      }
      return true;
    });
  }, [catalog, catalogSearch, catalogCategory]);

  function openCreate() {
    resetCustomForm();
    setEditTarget(null);
    setShowForm(true);
  }

  function openEdit(server: MCPServer) {
    setCustomForm({
      name: server.name,
      type: server.type,
      command: server.command ?? '',
      args: server.args ?? [],
      url: server.url ?? '',
      forward_headers: server.forward_headers ?? [],
      auth_type: server.auth_type ?? 'none',
      auth_key_env: server.auth_key_env ?? '',
      auth_client_id: server.auth_client_id ?? '',
      catalog_refresh_interval_seconds: server.catalog_refresh_interval_seconds ?? null,
    });
    setArgsInput((server.args ?? []).join('\n'));
    setEnvInput(server.env_vars ?? {});
    setForwardHeadersInput((server.forward_headers ?? []).join('\n'));
    setAuthType(server.auth_type ?? 'none');
    setAuthEnvVar(server.auth_key_env ?? '');
    setAuthClientId(server.auth_client_id ?? '');
    setRefreshIntervalInput(server.catalog_refresh_interval_seconds != null ? String(server.catalog_refresh_interval_seconds) : '');
    setEditTarget(server);
    setShowForm(true);
  }

  function closeForm() {
    setShowForm(false);
    setEditTarget(null);
    resetCustomForm();
  }

  function resetCustomForm() {
    setCustomForm({ ...emptyForm });
    setArgsInput('');
    setEnvInput({});
    setAuthType('none');
    setAuthEnvVar('');
    setAuthClientId('');
    setForwardHeadersInput('');
    setRefreshIntervalInput('');
  }

  function buildPayload(): CreateMCPServerRequest {
    const fh = forwardHeadersInput ? forwardHeadersInput.split('\n').map((h) => h.trim()).filter(Boolean) : [];
    // Empty string → null (disable refresh). Numeric string → integer (server
    // validates 30..86400; out-of-range surfaces as a 400 in handleSubmit).
    const refreshSeconds: number | null = refreshIntervalInput.trim() === ''
      ? null
      : Number(refreshIntervalInput.trim());
    return {
      ...customForm,
      args: argsInput ? argsInput.split('\n').map((a) => a.trim()).filter(Boolean) : [],
      env_vars: Object.keys(envInput).length > 0 ? envInput : undefined,
      forward_headers: fh.length > 0 ? fh : undefined,
      auth_type: authType !== 'none' ? authType : undefined,
      auth_key_env: authEnvVar || undefined,
      auth_client_id: authClientId || undefined,
      catalog_refresh_interval_seconds: refreshSeconds,
    };
  }

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setSaving(true);
    try {
      const payload = buildPayload();
      if (editTarget) {
        await api.updateMCPServer(editTarget.name, payload);
      } else {
        await api.createMCPServer(payload);
      }
      closeForm();
      setSelected(null);
      refetch();
    } catch {
      // visible in console
    } finally {
      setSaving(false);
    }
  }

  async function handleInstallCatalogEntry(entry: MCPCatalogEntry) {
    const pkg: MCPCatalogPackage | undefined = entry.packages.find(p => p.type === 'stdio') ?? entry.packages[0];
    if (!pkg) return;

    setSaving(true);
    try {
      const envVars: Record<string, string> = {};
      for (const ev of pkg.env_vars ?? []) {
        const val = envInput[ev.name];
        if (val) envVars[ev.name] = val;
      }

      const serverType = pkg.type === 'remote' ? (pkg.transport ?? 'streamable-http') : pkg.type;

      await api.createMCPServer({
        name: entry.name,
        type: serverType === 'docker' ? 'stdio' : serverType,
        command: pkg.command,
        args: pkg.args,
        url: pkg.url_template,
        env_vars: Object.keys(envVars).length > 0 ? envVars : undefined,
      });
      setShowCatalog(false);
      setEnvInput({});
      setCatalogDetail(null);
      setSelectedPkgIdx(0);
      refetch();
    } catch {
      // visible in console
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete() {
    if (!deleteTarget) return;
    try {
      await api.deleteMCPServer(deleteTarget);
      setDeleteTarget(null);
      setSelected(null);
      refetch();
    } catch {
      // visible in console
    }
  }

  const alreadyAdded = new Set((servers ?? []).map((s) => s.name));
  const isEdit = editTarget !== null;

  const columns = [
    { key: 'name', header: 'Name' },
    {
      key: 'type',
      header: 'Type',
      render: (row: MCPServer) => (
        <span className="px-2 py-0.5 bg-brand-light rounded text-xs text-brand-shade3 font-medium">
          {row.type}
        </span>
      ),
    },
    {
      key: 'command',
      header: 'Command / URL',
      render: (row: MCPServer) => (
        <span className="font-mono text-xs text-brand-shade3">
          {row.command ? `${row.command} ${(row.args ?? []).join(' ')}` : row.url ?? '--'}
        </span>
      ),
    },
    {
      key: 'status',
      header: 'Status',
      render: (row: MCPServer) =>
        row.status ? <StatusBadge status={row.status.status} /> : <span className="text-brand-shade3 text-xs">--</span>,
    },
    {
      key: 'circuit',
      header: 'Circuit',
      render: (row: MCPServer) => {
        const cb = circuitStateMap[row.name];
        if (!cb || cb.state === 'closed') {
          return <span className="inline-flex items-center gap-1 text-xs text-emerald-400">
            <span className="w-2 h-2 rounded-full bg-emerald-400 inline-block" />
            closed
          </span>;
        }
        if (cb.state === 'half_open') {
          return <span className="inline-flex items-center gap-1 text-xs text-amber-400">
            <span className="w-2 h-2 rounded-full bg-amber-400 inline-block" />
            half-open
          </span>;
        }
        return <span className="inline-flex items-center gap-1 text-xs text-red-400">
          <span className="w-2 h-2 rounded-full bg-red-400 inline-block" />
          open ({cb.failure_count})
        </span>;
      },
    },
    {
      key: 'tools_count',
      header: 'Tools',
      render: (row: MCPServer) => (
        <span className="text-xs">{row.status?.tools_count ?? '--'}</span>
      ),
    },
  ];

  if (loading) return <div className="text-brand-shade3">Loading MCP servers...</div>;
  if (error) return <div className="text-red-400">Error: {error}</div>;

  // Resolve the selected package for catalog detail view
  const detailPkg = catalogDetail?.packages[selectedPkgIdx] ?? catalogDetail?.packages[0];

  return (
    <PageContainer>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold text-brand-light">MCP Servers</h1>
        <div className="flex gap-2">
          <button
            onClick={() => setShowCatalog(true)}
            className="px-4 py-2 bg-brand-dark text-brand-light rounded-btn text-sm font-medium hover:bg-brand-dark-alt transition-colors"
          >
            Add from Catalog
          </button>
          <button
            onClick={openCreate}
            className="px-4 py-2 bg-brand-accent text-brand-light rounded-btn text-sm font-medium hover:bg-brand-accent-hover transition-colors"
          >
            Add Custom
          </button>
        </div>
      </div>

      <div className="bg-brand-dark-alt rounded-card border border-brand-shade3/15">
        <DataTable
          columns={columns}
          data={servers ?? []}
          keyField="name"
          onRowClick={setSelected}
          activeKey={selected?.name}
          emptyMessage="No MCP servers configured"
          emptyIcon={emptyIcons.mcp}
          emptyAction={{ label: 'Add from Catalog', onClick: () => setShowCatalog(true) }}
        />
      </div>

      {/* Detail Panel */}
      <DetailPanel
        open={selected !== null}
        onClose={() => setSelected(null)}
        title={selected?.name ?? ''}
        actions={
          selected ? (
            <>
              <button
                onClick={() => openEdit(selected)}
                className="flex-1 px-4 py-2 bg-brand-accent text-brand-light rounded-btn text-sm font-medium hover:bg-brand-accent-hover transition-colors"
              >
                Edit
              </button>
              <button
                onClick={async () => {
                  const target = selected.name;
                  setRefreshing(target);
                  try {
                    const result = await api.refreshMCPServer(target);
                    addToast(`Refreshed ${target}: ${result.tools_count} tools`, 'success');
                    refetch();
                  } catch (err) {
                    addToast(`Refresh failed: ${err instanceof Error ? err.message : String(err)}`, 'error');
                  } finally {
                    setRefreshing(null);
                  }
                }}
                disabled={refreshing === selected.name}
                title="Re-query tools/list now without reconnecting the transport"
                className="px-4 py-2 text-blue-400 border border-blue-500/30 rounded-btn text-sm font-medium hover:bg-blue-500/10 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              >
                {refreshing === selected.name ? 'Refreshing…' : 'Refresh'}
              </button>
              {(() => {
                const cb = circuitStateMap[selected.name];
                const isClosed = !cb || cb.state === 'closed';
                return (
                  <button
                    onClick={async () => {
                      setResetting(true);
                      try {
                        await api.resetCircuitBreaker(selected.name);
                      } finally {
                        refetchCircuitBreakers();
                        setResetting(false);
                      }
                    }}
                    disabled={resetting || isClosed}
                    title={isClosed ? 'Breaker is closed — nothing to reset' : 'Force-close the open circuit breaker'}
                    className="px-4 py-2 text-amber-400 border border-amber-500/30 rounded-btn text-sm font-medium hover:bg-amber-500/10 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                  >
                    {resetting ? 'Resetting…' : 'Reset Breaker'}
                  </button>
                );
              })()}
              <button
                onClick={() => setDeleteTarget(selected.name)}
                className="px-4 py-2 text-red-400 border border-red-500/30 rounded-btn text-sm font-medium hover:bg-red-500/10 transition-colors"
              >
                Remove
              </button>
            </>
          ) : undefined
        }
      >
        {selected && (
          <>
            <DetailSection title="Configuration">
              <DetailRow label="Type">
                <span className="px-2 py-0.5 bg-brand-light rounded text-xs text-brand-shade3 font-medium">{selected.type}</span>
              </DetailRow>
              {selected.command && (
                <DetailRow label="Command">
                  <code className="font-mono text-xs">{selected.command} {(selected.args ?? []).join(' ')}</code>
                </DetailRow>
              )}
              {selected.url && (
                <DetailRow label="URL">
                  <code className="font-mono text-xs">{selected.url}</code>
                </DetailRow>
              )}
            </DetailSection>

            {selected.status && (
              <DetailSection title="Runtime Status">
                <DetailRow label="Status"><StatusBadge status={selected.status.status} /></DetailRow>
                {(() => {
                  const cb = circuitStateMap[selected.name];
                  if (!cb) return null;
                  return (
                    <DetailRow label="Circuit State">
                      {cb.state === 'closed'
                        ? <span className="text-emerald-400 text-sm">● Closed</span>
                        : cb.state === 'half_open'
                          ? <span className="text-amber-400 text-sm">● Half-Open</span>
                          : <span className="text-red-400 text-sm">● Open ({cb.failure_count} failures)</span>}
                    </DetailRow>
                  );
                })()}
                <DetailRow label="Tools Count">{selected.status.tools_count}</DetailRow>
                {selected.status.connected_at && (
                  <DetailRow label="Connected At">{new Date(selected.status.connected_at).toLocaleString()}</DetailRow>
                )}
                {selected.status.status_message && (
                  <DetailRow label="Message">{selected.status.status_message}</DetailRow>
                )}
              </DetailSection>
            )}

            {selected.agents.length > 0 && (
              <DetailSection title="Used by Agents">
                <div className="flex flex-wrap gap-1.5">
                  {selected.agents.map((a) => (
                    <button
                      key={a}
                      onClick={() => navigate(`/schemas/default/${encodeURIComponent(a)}`)}
                      className="px-2 py-0.5 bg-blue-500/10 border border-blue-500/25 rounded text-xs text-blue-400 hover:bg-blue-500/20 hover:border-blue-500/40 transition-colors cursor-pointer"
                      title={`Go to ${a} detail`}
                    >
                      {a}
                    </button>
                  ))}
                </div>
              </DetailSection>
            )}

            {selected.env_vars && Object.keys(selected.env_vars).length > 0 && (
              <DetailSection title="Environment Variables">
                {Object.entries(selected.env_vars).map(([key, value]) => (
                  <DetailRow key={key} label={key}>
                    <code className="font-mono text-xs">{value ? '***' : '(empty)'}</code>
                  </DetailRow>
                ))}
              </DetailSection>
            )}
          </>
        )}
      </DetailPanel>

      {/* MCP Catalog modal — with search, category filter, detail view */}
      <Modal
        open={showCatalog}
        onClose={() => { setShowCatalog(false); setEnvInput({}); setCatalogSearch(''); setCatalogCategory('all'); setCatalogDetail(null); setSelectedPkgIdx(0); }}
        title="MCP Catalog"
      >
        <div className="space-y-3">
          {/* Search */}
          <input
            type="text"
            value={catalogSearch}
            onChange={(e) => setCatalogSearch(e.target.value)}
            placeholder="Search servers..."
            className="w-full px-3 py-2 bg-brand-dark-alt border border-brand-shade3/30 rounded-card text-sm text-brand-light placeholder-brand-shade3 focus:outline-none focus:border-brand-accent transition-colors"
          />

          {/* Category filter */}
          <div className="flex gap-1.5 flex-wrap">
            {(Object.entries(CATEGORY_META) as [MCPCatalogCategory | 'all', { label: string; color: string }][]).map(([key, meta]) => (
              <button
                key={key}
                onClick={() => setCatalogCategory(key)}
                className={`px-2.5 py-1 rounded-btn text-[11px] font-medium transition-colors ${
                  catalogCategory === key
                    ? 'bg-brand-accent text-brand-light'
                    : `${meta.color} hover:opacity-80`
                }`}
              >
                {meta.label}
              </button>
            ))}
          </div>

          {/* Server list or detail */}
          {catalogDetail ? (
            <div className="space-y-3">
              <button
                onClick={() => { setCatalogDetail(null); setEnvInput({}); setSelectedPkgIdx(0); }}
                className="text-xs text-brand-accent hover:underline flex items-center gap-1"
              >
                <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <polyline points="15 18 9 12 15 6" />
                </svg>
                Back to list
              </button>
              <div className="border border-brand-shade3/30 rounded-card p-4 space-y-3">
                <div className="flex items-center justify-between">
                  <div>
                    <div className="flex items-center gap-2">
                      <h3 className="font-semibold text-brand-light text-sm">{catalogDetail.display}</h3>
                      {catalogDetail.verified && (
                        <span className="px-1.5 py-0.5 bg-emerald-500/15 text-emerald-400 rounded text-[9px] font-medium">Verified</span>
                      )}
                    </div>
                    <span className="text-xs text-brand-shade3 font-mono">{catalogDetail.name}</span>
                  </div>
                  {catalogDetail.category && (
                    <span className={`px-2 py-0.5 rounded text-[10px] font-medium ${CATEGORY_META[catalogDetail.category]?.color ?? CATEGORY_META.generic.color}`}>
                      {CATEGORY_META[catalogDetail.category]?.label ?? catalogDetail.category}
                    </span>
                  )}
                </div>

                {catalogDetail.description && (
                  <p className="text-xs text-brand-shade3">{catalogDetail.description}</p>
                )}

                {/* Package selector (if multiple) */}
                {catalogDetail.packages.length > 1 && (
                  <div>
                    <p className="text-xs text-brand-shade3 mb-1">Transport</p>
                    <div className="flex gap-1.5">
                      {catalogDetail.packages.map((pkg, idx) => {
                        const pkgLabel = pkg.type === 'remote' ? (pkg.transport ?? 'remote') : pkg.type;
                        return (
                          <button
                            key={idx}
                            onClick={() => { setSelectedPkgIdx(idx); setEnvInput({}); }}
                            className={`px-2.5 py-1 rounded-btn text-[11px] font-medium transition-colors ${
                              selectedPkgIdx === idx
                                ? 'bg-brand-accent text-brand-light'
                                : 'bg-brand-shade3/10 text-brand-shade2 hover:opacity-80'
                            }`}
                          >
                            {pkgLabel}
                          </button>
                        );
                      })}
                    </div>
                  </div>
                )}

                {/* Package details */}
                {detailPkg && (
                  <>
                    {detailPkg.type === 'stdio' && detailPkg.command && (
                      <div>
                        <p className="text-xs text-brand-shade3 mb-1">Command</p>
                        <code className="text-xs text-brand-light font-mono">{detailPkg.command} {(detailPkg.args ?? []).join(' ')}</code>
                      </div>
                    )}
                    {detailPkg.type === 'remote' && detailPkg.url_template && (
                      <div>
                        <p className="text-xs text-brand-shade3 mb-1">URL</p>
                        <code className="text-xs text-brand-light font-mono">{detailPkg.url_template}</code>
                      </div>
                    )}
                    {detailPkg.type === 'docker' && detailPkg.image && (
                      <div>
                        <p className="text-xs text-brand-shade3 mb-1">Docker Image</p>
                        <code className="text-xs text-brand-light font-mono">{detailPkg.image}</code>
                      </div>
                    )}

                    {(detailPkg.env_vars ?? []).length > 0 && (
                      <div>
                        <p className="text-xs text-brand-shade3 mb-1">Environment Variables</p>
                        <div className="space-y-1.5">
                          {(detailPkg.env_vars ?? []).map((ev) => (
                            <div key={ev.name}>
                              <div className="flex items-center gap-1 mb-0.5">
                                <span className="text-[10px] font-mono text-brand-shade2">{ev.name}</span>
                                {ev.required && <span className="text-[9px] text-red-400">*</span>}
                              </div>
                              {ev.description && (
                                <p className="text-[10px] text-brand-shade3/60 mb-0.5">{ev.description}</p>
                              )}
                              <input
                                type={ev.secret ? 'password' : 'text'}
                                placeholder={ev.name}
                                value={envInput[ev.name] ?? ''}
                                onChange={(e) => setEnvInput((prev) => ({ ...prev, [ev.name]: e.target.value }))}
                                className="w-full px-2.5 py-1.5 bg-brand-dark border border-brand-shade3/30 rounded-card text-xs text-brand-light placeholder-brand-shade3 font-mono focus:outline-none focus:border-brand-accent transition-colors"
                              />
                            </div>
                          ))}
                        </div>
                      </div>
                    )}
                  </>
                )}

                {/* Provided tools */}
                {catalogDetail.provided_tools && catalogDetail.provided_tools.length > 0 && (
                  <div>
                    <p className="text-xs text-brand-shade3 mb-1">Provided Tools</p>
                    <div className="space-y-1">
                      {catalogDetail.provided_tools.map((tool) => (
                        <div key={tool.name} className="flex items-start gap-2">
                          <code className="text-[10px] text-brand-accent font-mono whitespace-nowrap">{tool.name}</code>
                          <span className="text-[10px] text-brand-shade3">{tool.description}</span>
                        </div>
                      ))}
                    </div>
                  </div>
                )}

                <button
                  onClick={() => handleInstallCatalogEntry(catalogDetail)}
                  disabled={alreadyAdded.has(catalogDetail.name) || saving}
                  className="w-full py-2 bg-brand-accent text-brand-light rounded-btn text-sm font-medium hover:bg-brand-accent-hover disabled:opacity-50 transition-colors"
                >
                  {alreadyAdded.has(catalogDetail.name) ? 'Already Added' : saving ? 'Installing...' : 'Install'}
                </button>
              </div>
            </div>
          ) : (
            <div className="space-y-2 max-h-80 overflow-y-auto">
              {filteredCatalog.map((entry) => {
                const added = alreadyAdded.has(entry.name);
                const primaryPkg = entry.packages.find(p => p.type === 'stdio') ?? entry.packages[0];
                return (
                  <div
                    key={entry.name}
                    className={`border border-brand-shade3/20 rounded-card p-3 hover:border-brand-shade3/40 transition-colors cursor-pointer ${added ? 'opacity-50' : ''}`}
                    onClick={() => { setCatalogDetail(entry); setSelectedPkgIdx(0); setEnvInput({}); }}
                  >
                    <div className="flex items-center justify-between">
                      <div className="flex items-center gap-2">
                        <span className="font-medium text-brand-light text-sm">{entry.display}</span>
                        {entry.verified && (
                          <span className="px-1 py-0.5 bg-emerald-500/15 text-emerald-400 rounded text-[8px] font-medium">Verified</span>
                        )}
                        {entry.category && (
                          <span className={`px-1.5 py-0.5 rounded text-[9px] font-medium ${CATEGORY_META[entry.category]?.color ?? CATEGORY_META.generic.color}`}>
                            {CATEGORY_META[entry.category]?.label ?? entry.category}
                          </span>
                        )}
                      </div>
                      {added && (
                        <span className="text-[10px] text-brand-shade3">Added</span>
                      )}
                    </div>
                    {entry.description ? (
                      <div className="text-[11px] text-brand-shade3 mt-1 truncate">
                        {entry.description}
                      </div>
                    ) : primaryPkg?.command ? (
                      <div className="text-[11px] text-brand-shade3 mt-1 font-mono truncate">
                        {primaryPkg.command} {(primaryPkg.args ?? []).join(' ')}
                      </div>
                    ) : primaryPkg?.url_template ? (
                      <div className="text-[11px] text-brand-shade3 mt-1 font-mono truncate">
                        {primaryPkg.url_template}
                      </div>
                    ) : null}
                    {primaryPkg?.env_vars && primaryPkg.env_vars.length > 0 && (
                      <div className="text-[10px] text-brand-shade3/60 mt-1">
                        Requires: {primaryPkg.env_vars.map(ev => ev.name).join(', ')}
                      </div>
                    )}
                  </div>
                );
              })}
              {filteredCatalog.length === 0 && (
                <p className="text-sm text-brand-shade3 text-center py-4">
                  {catalogSearch ? 'No servers match your search.' : 'No catalog servers available.'}
                </p>
              )}
            </div>
          )}
        </div>
      </Modal>

      {/* Custom add / edit modal */}
      <FormModal
        open={showForm}
        onClose={closeForm}
        title={isEdit ? 'Edit MCP Server' : 'Add Custom MCP Server'}
        onSubmit={handleSubmit}
        submitLabel={isEdit ? 'Save Changes' : 'Add Server'}
        loading={saving}
      >
        <FormField
          label="Name"
          value={customForm.name}
          onChange={(v) => setCustomForm({ ...customForm, name: v })}
          required
          disabled={isEdit}
          hint={isEdit ? 'Name cannot be changed.' : undefined}
        />
        <FormField
          label="Transport"
          type="select"
          value={customForm.type}
          onChange={(v) => setCustomForm({ ...customForm, type: v })}
          options={[
            { value: 'stdio', label: 'stdio — Local process' },
            { value: 'streamable-http', label: 'streamable-http — HTTP streaming' },
            { value: 'sse', label: 'sse — Server-Sent Events' },
            { value: 'http', label: 'http — HTTP' },
          ]}
        />
        {customForm.type === 'stdio' && (
          <>
            <FormField
              label="Command"
              value={customForm.command ?? ''}
              onChange={(v) => setCustomForm({ ...customForm, command: v })}
              placeholder="npx"
            />
            <FormField
              label="Args (one per line)"
              type="textarea"
              value={argsInput}
              onChange={setArgsInput}
              rows={3}
              placeholder="@anthropic/playwright-mcp"
            />
          </>
        )}
        {(customForm.type === 'http' || customForm.type === 'sse' || customForm.type === 'streamable-http') && (
          <FormField
            label="URL"
            value={customForm.url ?? ''}
            onChange={(v) => setCustomForm({ ...customForm, url: v })}
            placeholder="http://localhost:3000/mcp"
          />
        )}
        <div>
          <label className="block text-sm font-medium text-brand-light mb-1">Environment Variables</label>
          <div className="space-y-2">
            {Object.entries(envInput).map(([key, value]) => (
              <div key={key} className="flex items-center gap-2">
                <input
                  type="text"
                  value={key}
                  readOnly
                  className="w-1/3 px-2 py-1.5 bg-brand-dark border border-brand-shade3/50 rounded-btn text-xs text-brand-shade2"
                />
                <input
                  type="text"
                  value={value}
                  onChange={(e) => setEnvInput((prev) => ({ ...prev, [key]: e.target.value }))}
                  className="flex-1 px-2 py-1.5 bg-brand-dark-alt border border-brand-shade3/50 rounded-btn text-xs text-brand-light focus:outline-none focus:border-brand-accent"
                />
                <button
                  type="button"
                  onClick={() => {
                    const next = { ...envInput };
                    delete next[key];
                    setEnvInput(next);
                  }}
                  className="text-red-500 hover:text-red-700 text-xs p-1"
                >
                  <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
                  </svg>
                </button>
              </div>
            ))}
            <button
              type="button"
              onClick={() => {
                const key = prompt('Variable name:');
                if (key && key.trim()) {
                  setEnvInput((prev) => ({ ...prev, [key.trim()]: '' }));
                }
              }}
              className="text-xs text-brand-accent hover:underline"
            >
              + Add variable
            </button>
          </div>
        </div>
        <div>
          <label className="block text-sm font-medium text-brand-light mb-1">Authentication</label>
          <FormField
            label="Auth Type"
            type="select"
            value={authType}
            onChange={setAuthType}
            options={[
              { value: 'none', label: 'None' },
              { value: 'forward_headers', label: 'Forward Headers' },
              { value: 'api_key', label: 'API Key' },
              { value: 'oauth2', label: 'OAuth 2.0' },
              { value: 'service_account', label: 'Service Account' },
            ]}
          />
          {authType === 'api_key' && (
            <FormField
              label="API Key Env Variable"
              value={authEnvVar}
              onChange={setAuthEnvVar}
              placeholder="e.g. SHEETS_API_KEY"
              hint="Name of the environment variable containing the API key"
              className="mt-2"
            />
          )}
          {authType === 'oauth2' && (
            <>
              <FormField
                label="Client ID"
                value={authClientId}
                onChange={setAuthClientId}
                placeholder="OAuth client ID"
                className="mt-2"
              />
              <p className="mt-1 text-xs text-brand-shade3">OAuth flow configured via admin. Tokens are managed automatically.</p>
            </>
          )}
          {authType === 'forward_headers' && (
            <div className="mt-2">
              <label className="block text-xs font-medium text-brand-shade2 mb-1">Headers to Forward (one per line)</label>
              <textarea
                value={forwardHeadersInput}
                onChange={(e) => setForwardHeadersInput(e.target.value)}
                rows={3}
                className="w-full px-3 py-2 bg-brand-bg border border-brand-shade3/30 rounded-btn text-sm text-brand-light placeholder:text-brand-shade3 focus:outline-none focus:border-brand-accent"
                placeholder={"Authorization\nX-Tenant-ID"}
              />
              <p className="mt-1 text-xs text-brand-shade3">Only these headers from the calling request will be forwarded to this MCP server.</p>
            </div>
          )}
          {authType === 'service_account' && (
            <FormField
              label="Credentials Env Variable"
              value={authEnvVar}
              onChange={setAuthEnvVar}
              placeholder="e.g. GCP_SERVICE_ACCOUNT_JSON"
              hint="Name of the environment variable containing service account credentials"
              className="mt-2"
            />
          )}
        </div>
        <div>
          <label htmlFor="mcp-refresh-interval" className="block text-sm font-medium text-brand-light mb-1">
            Catalog refresh interval (seconds)
          </label>
          <input
            id="mcp-refresh-interval"
            type="number"
            min={30}
            max={86400}
            value={refreshIntervalInput}
            onChange={(e) => setRefreshIntervalInput(e.target.value)}
            placeholder="empty = disabled"
            className="w-full px-3 py-2 bg-brand-bg border border-brand-shade3/30 rounded-btn text-sm text-brand-light placeholder:text-brand-shade3 focus:outline-none focus:border-brand-accent"
          />
          <p className="mt-1 text-xs text-brand-shade3">
            Auto-refresh the tool catalog every N seconds (30–86400). Empty = disabled (default).
          </p>
        </div>
      </FormModal>

      <ConfirmDialog
        open={deleteTarget !== null}
        onClose={() => setDeleteTarget(null)}
        onConfirm={handleDelete}
        title="Remove MCP Server"
        message={
          <>
            Remove MCP server <strong className="text-brand-dark">{deleteTarget}</strong>?
          </>
        }
        confirmLabel="Remove"
        variant="danger"
      />
    </PageContainer>
  );
}
