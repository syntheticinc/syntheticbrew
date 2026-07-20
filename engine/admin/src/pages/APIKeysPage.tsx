import { useState, type FormEvent } from 'react';
import { api } from '../api/client';
import { useApi } from '../hooks/useApi';
import DataTable from '../components/DataTable';
import Modal from '../components/Modal';
import ConnectClaudeCode from '../components/ConnectClaudeCode';
import type { APIToken } from '../types';

const SCOPES = [
  { bit: 1, label: 'Chat', description: 'POST /schemas/{name}/chat — all chat-enabled schemas' },
  { bit: 2, label: 'Tasks', description: 'CRUD /tasks' },
  { bit: 4, label: 'Agents Read', description: 'GET /agents' },
  { bit: 32, label: 'Agents Write', description: 'POST/PUT/DELETE /agents' },
  { bit: 64, label: 'Models Read', description: 'GET /models' },
  { bit: 128, label: 'Models Write', description: 'POST/PUT/DELETE /models' },
  { bit: 256, label: 'MCP Read', description: 'GET /mcp-servers' },
  { bit: 512, label: 'MCP Write', description: 'POST/PUT/DELETE /mcp-servers' },
  { bit: 8, label: 'Config', description: 'POST /config/reload, import/export' },
  { bit: 16, label: 'Admin', description: 'Full access (all operations)' },
];

function scopesToLabels(mask: number): string {
  return SCOPES.filter((s) => (mask & s.bit) !== 0)
    .map((s) => s.label)
    .join(', ') || 'None';
}

export default function APIKeysPage() {
  const { data: tokens, loading, error, refetch } = useApi(() => api.listTokens());

  const [showCreate, setShowCreate] = useState(false);
  const [name, setName] = useState('');
  const [scopesMask, setScopesMask] = useState(0);
  const [saving, setSaving] = useState(false);
  const [createdToken, setCreatedToken] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);

  function toggleScope(bit: number) {
    setScopesMask((prev) => prev ^ bit);
  }

  async function handleCreate(e: FormEvent) {
    e.preventDefault();
    setSaving(true);
    try {
      const res = await api.createToken({ name, scopes_mask: scopesMask });
      setCreatedToken(res.token);
      setName('');
      setScopesMask(0);
      refetch();
    } catch {
      // visible in console
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete() {
    if (deleteTarget === null) return;
    try {
      await api.deleteToken(deleteTarget);
      setDeleteTarget(null);
      refetch();
    } catch {
      // visible in console
    }
  }

  async function copyToken() {
    if (!createdToken) return;
    await navigator.clipboard.writeText(createdToken);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  }

  const columns = [
    { key: 'name', header: 'Name' },
    {
      key: 'scopes_mask',
      header: 'Scopes',
      render: (row: APIToken) => (
        <span className="text-xs">{scopesToLabels(row.scopes_mask)}</span>
      ),
    },
    {
      key: 'created_at',
      header: 'Created',
      render: (row: APIToken) => (
        <span className="text-xs text-brand-shade3">
          {new Date(row.created_at).toLocaleDateString()}
        </span>
      ),
    },
    {
      key: 'last_used_at',
      header: 'Last Used',
      render: (row: APIToken) =>
        row.last_used_at ? (
          <span className="text-xs text-brand-shade3">
            {new Date(row.last_used_at).toLocaleDateString()}
          </span>
        ) : (
          <span className="text-xs text-brand-shade3">Never</span>
        ),
    },
    {
      key: 'actions',
      header: '',
      render: (row: APIToken) => (
        <button
          onClick={(e) => {
            e.stopPropagation();
            setDeleteTarget(row.id);
          }}
          className="text-red-400 hover:text-red-300 text-sm"
        >
          Revoke
        </button>
      ),
    },
  ];

  if (loading) return <div className="text-brand-shade3">Loading tokens...</div>;
  if (error) return <div className="text-red-400">Error: {error}</div>;

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold text-brand-light">API Keys</h1>
        <button
          onClick={() => setShowCreate(true)}
          className="px-4 py-2 bg-brand-accent text-white rounded-btn text-sm font-medium hover:bg-brand-accent-hover transition-colors"
        >
          Generate New Token
        </button>
      </div>

      <p className="text-xs text-brand-shade3 mb-2">
        Alternative entry point: the Agents page can onboard a coding agent in
        one click — the setup below is the manual, per-client route.
      </p>
      <ConnectClaudeCode onMinted={refetch} />

      <div className="bg-brand-dark-alt rounded-card border border-brand-shade3/15">
        <DataTable
          columns={columns}
          data={tokens ?? []}
          keyField="id"
          emptyMessage="No API tokens. Generate your first token."
        />
      </div>

      {/* Create token modal */}
      <Modal
        open={showCreate && !createdToken}
        onClose={() => {
          setShowCreate(false);
          setName('');
          setScopesMask(0);
        }}
        title="Generate API Token"
      >
        <form onSubmit={handleCreate} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-brand-light mb-1">Token Name</label>
            <input
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              required
              placeholder="my-integration"
              className="w-full px-3 py-2 bg-brand-dark-alt border border-brand-shade3/50 rounded-btn text-sm text-brand-light placeholder-brand-shade3 focus:outline-none focus:border-brand-accent"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-brand-light mb-2">Scopes</label>
            <div className="space-y-2">
              {SCOPES.map((s) => (
                <label key={s.bit} className="flex items-center gap-2 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={(scopesMask & s.bit) !== 0}
                    onChange={() => toggleScope(s.bit)}
                    className="rounded border-brand-shade3/30 text-brand-accent focus:ring-brand-accent bg-brand-dark"
                  />
                  <span className="text-sm text-brand-light">{s.label}</span>
                  <span className="text-xs text-brand-shade3">({s.description})</span>
                </label>
              ))}
            </div>
          </div>
          <div className="flex justify-end gap-2 pt-2">
            <button
              type="button"
              onClick={() => {
                setShowCreate(false);
                setName('');
                setScopesMask(0);
              }}
              className="px-4 py-2 text-sm text-brand-shade2 border border-brand-shade3/30 rounded-btn hover:bg-brand-dark hover:text-brand-light transition-colors"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={saving || !name}
              className="px-4 py-2 text-sm text-white bg-brand-accent rounded-btn hover:bg-brand-accent-hover disabled:opacity-50"
            >
              {saving ? 'Generating...' : 'Generate'}
            </button>
          </div>
        </form>
      </Modal>

      {/* Token created - show once */}
      <Modal
        open={createdToken !== null}
        onClose={() => {
          setCreatedToken(null);
          setShowCreate(false);
        }}
        title="Token Created"
      >
        <div className="space-y-4">
          <div className="p-3 bg-yellow-500/10 border border-yellow-500/30 rounded-btn text-sm text-yellow-400">
            Save this token now. It will not be shown again.
          </div>
          <div className="flex items-center gap-2">
            <input
              type="text"
              value={createdToken ?? ''}
              readOnly
              className="flex-1 px-3 py-2 border border-brand-shade3/30 rounded-btn text-sm font-mono bg-brand-dark text-brand-light"
            />
            <button
              onClick={copyToken}
              className="px-3 py-2 text-sm bg-brand-dark border border-brand-shade3/30 rounded-btn text-brand-shade2 hover:text-brand-light transition-colors"
            >
              {copied ? 'Copied!' : 'Copy'}
            </button>
          </div>
        </div>
      </Modal>

      {/* Delete confirmation */}
      <Modal
        open={deleteTarget !== null}
        onClose={() => setDeleteTarget(null)}
        title="Revoke Token"
        footer={
          <>
            <button
              onClick={() => setDeleteTarget(null)}
              className="px-4 py-2 text-sm text-brand-shade2 border border-brand-shade3/30 rounded-btn hover:bg-brand-dark hover:text-brand-light transition-colors"
            >
              Cancel
            </button>
            <button
              onClick={handleDelete}
              className="px-4 py-2 text-sm text-white bg-red-600 rounded-btn hover:bg-red-700"
            >
              Revoke
            </button>
          </>
        }
      >
        <p className="text-sm text-brand-shade2">
          Revoke this API token? Any integrations using this token will stop working.
        </p>
      </Modal>
    </div>
  );
}
