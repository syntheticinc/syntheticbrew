import { useState, useRef, useCallback, useMemo, useEffect, type FormEvent } from 'react';
import { api } from '../api/client';
import { useApi } from '../hooks/useApi';
import { useAdminRefresh } from '../hooks/useAdminRefresh';
import DataTable from '../components/DataTable';
import { emptyIcons } from '../components/EmptyState';
import DetailPanel, { DetailRow, DetailSection } from '../components/DetailPanel';
import FormModal from '../components/FormModal';
import FormField from '../components/FormField';
import ConfirmDialog from '../components/ConfirmDialog';
import PageContainer from '../components/PageContainer';
import type { KnowledgeBase, CreateKnowledgeBaseRequest, KnowledgeFile, KnowledgeFileStatus, Model, AgentInfo } from '../types';

// ─── Constants ──────────────────────────────────────────────────────────────

const ACCEPTED_TYPES = '.txt,.md,.csv,.pdf,.docx';

const emptyForm: CreateKnowledgeBaseRequest = {
  name: '',
  description: '',
  embedding_model_id: '',
};

// ─── Status badges ──────────────────────────────────────────────────────────

function formatFileStatus(status: KnowledgeFileStatus, error?: string) {
  switch (status) {
    case 'ready':
      return (
        <span className="inline-flex items-center px-2 py-0.5 text-xs font-medium rounded-full bg-emerald-500/15 text-emerald-400">
          Ready
        </span>
      );
    case 'indexing':
      return (
        <span className="inline-flex items-center px-2 py-0.5 text-xs font-medium rounded-full bg-amber-500/15 text-amber-400">
          Indexing
        </span>
      );
    case 'uploading':
      return (
        <span className="inline-flex items-center px-2 py-0.5 text-xs font-medium rounded-full bg-blue-500/15 text-blue-400">
          Uploading
        </span>
      );
    case 'error':
      return (
        <span
          className="inline-flex items-center px-2 py-0.5 text-xs font-medium rounded-full bg-red-500/15 text-red-400"
          title={error}
        >
          Error
        </span>
      );
    default:
      return null;
  }
}

// ─── Upload icon ────────────────────────────────────────────────────────────

const uploadIcon = (
  <svg
    className="w-8 h-8 text-brand-shade3/50"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth="1.5"
    strokeLinecap="round"
    strokeLinejoin="round"
  >
    <path d="M21 15v4a2 2 0 01-2 2H5a2 2 0 01-2-2v-4" />
    <polyline points="17 8 12 3 7 8" />
    <line x1="12" y1="3" x2="12" y2="15" />
  </svg>
);

// ─── Component ──────────────────────────────────────────────────────────────

export default function KnowledgePage() {
  // ── Data fetching ──
  const { data: knowledgeBases, loading, error, refetch } = useApi(() => api.listKnowledgeBases());
  useAdminRefresh(refetch);

  // Wave 5: KBs require an embedding-kind model. Filter server-side so the
  // dropdown never shows chat models (backend rejects them with
  // `embedding_model_id must reference an embedding model`).
  const { data: models } = useApi(() => api.listModels({ kind: 'embedding' }));
  const { data: agents } = useApi(() => api.listAgents());

  // ── Selection & panel state ──
  const [selected, setSelected] = useState<KnowledgeBase | null>(null);
  const [showForm, setShowForm] = useState(false);
  const [editTarget, setEditTarget] = useState<KnowledgeBase | null>(null);
  const [form, setForm] = useState<CreateKnowledgeBaseRequest>({ ...emptyForm });
  const [saving, setSaving] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<KnowledgeBase | null>(null);

  // ── Files state ──
  const [files, setFiles] = useState<KnowledgeFile[]>([]);
  const [filesLoading, setFilesLoading] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [dragOver, setDragOver] = useState(false);
  const [deletingFile, setDeletingFile] = useState<string | null>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);

  // ── Agent linking state ──
  const [linkingAgent, setLinkingAgent] = useState('');
  const [linkingSaving, setLinkingSaving] = useState(false);

  // ── Action error ──
  const [actionError, setActionError] = useState<string | null>(null);

  // ── Derived data ──
  // Wave 5: `models` is already filtered server-side to kind='embedding'. Keep
  // a local alias + defensive filter in case the API ever starts returning
  // chat models here (belt-and-braces — backend validation rejects those
  // anyway).
  const embeddingModels = useMemo(
    () => (models ?? []).filter((m: Model) => m.kind === 'embedding'),
    [models],
  );

  const modelsMap = useMemo(() => {
    const map = new Map<string, Model>();
    for (const m of models ?? []) {
      map.set(m.id, m);
    }
    return map;
  }, [models]);

  const agentsMap = useMemo(() => {
    const map = new Map<string, AgentInfo>();
    for (const a of agents ?? []) {
      map.set(a.name, a);
    }
    return map;
  }, [agents]);

  // Agents available for linking (not already linked to this KB)
  const availableAgents = useMemo(() => {
    if (!selected || !agents) return [];
    const linked = new Set(selected.linked_agents);
    return agents.filter((a: AgentInfo) => !linked.has(a.name));
  }, [selected, agents]);

  // ── Files loader ──
  const loadFiles = useCallback(async (kbName: string) => {
    setFilesLoading(true);
    try {
      const result = await api.listKBFiles(kbName);
      setFiles(result);
    } catch {
      setFiles([]);
    } finally {
      setFilesLoading(false);
    }
  }, []);

  // ── Poll file status when any file is indexing/uploading ──
  useEffect(() => {
    if (!selected) return;
    const hasInProgress = files.some(f => f.status === 'indexing' || f.status === 'uploading');
    if (!hasInProgress) return;
    const timer = setInterval(() => {
      api.listKBFiles(selected.name).then((updated) => {
        setFiles(updated);
        // Also refresh the KB row to update file_count
        api.getKnowledgeBase(selected.name).then((kb) => {
          setSelected(kb);
          refetch();
        }).catch(() => {});
      }).catch(() => {});
    }, 3000);
    return () => clearInterval(timer);
  }, [selected, files, refetch]);

  // ── Row selection ──
  function handleSelect(kb: KnowledgeBase) {
    setSelected(kb);
    setActionError(null);
    setLinkingAgent('');
    loadFiles(kb.name);
  }

  function handleClosePanel() {
    setSelected(null);
    setFiles([]);
    setActionError(null);
    setLinkingAgent('');
  }

  // ── Refresh selected KB after mutations ──
  async function refreshSelected(kbId: string) {
    refetch();
    const updatedList = await api.listKnowledgeBases();
    const updated = updatedList.find((kb) => kb.id === kbId);
    if (updated) {
      setSelected(updated);
      loadFiles(updated.name);
    }
  }

  // ── CRUD: Knowledge Bases ──
  function openCreate() {
    setForm({ ...emptyForm });
    setEditTarget(null);
    setShowForm(true);
  }

  function openEdit(kb: KnowledgeBase) {
    setForm({
      name: kb.name,
      description: kb.description ?? '',
      embedding_model_id: kb.embedding_model_id ?? '',
    });
    setEditTarget(kb);
    setShowForm(true);
  }

  function closeForm() {
    setShowForm(false);
    setEditTarget(null);
    setForm({ ...emptyForm });
  }

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setSaving(true);
    try {
      if (editTarget) {
        await api.updateKnowledgeBase(editTarget.name, form);
      } else {
        await api.createKnowledgeBase(form);
      }
      closeForm();
      setSelected(null);
      setFiles([]);
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
      await api.deleteKnowledgeBase(deleteTarget.name);
      setDeleteTarget(null);
      setSelected(null);
      setFiles([]);
      refetch();
    } catch {
      // visible in console
    }
  }

  // ── Files management ──
  async function handleUpload(fileList: FileList | null) {
    if (!fileList || fileList.length === 0 || !selected || uploading) return;
    setUploading(true);
    setActionError(null);
    try {
      for (let i = 0; i < fileList.length; i++) {
        await api.uploadKBFile(selected.name, fileList[i]!);
      }
      await refreshSelected(selected.id);
    } catch (err: unknown) {
      setActionError(err instanceof Error ? err.message : 'Failed to upload file');
    } finally {
      setUploading(false);
      if (fileInputRef.current) {
        fileInputRef.current.value = '';
      }
    }
  }

  function handleDrop(e: React.DragEvent) {
    e.preventDefault();
    setDragOver(false);
    handleUpload(e.dataTransfer.files);
  }

  async function handleDeleteFile(fileId: string) {
    if (!selected || deletingFile) return;
    setDeletingFile(fileId);
    setActionError(null);
    try {
      await api.deleteKBFile(selected.name, fileId);
      await refreshSelected(selected.id);
    } catch (err: unknown) {
      setActionError(err instanceof Error ? err.message : 'Failed to delete file');
    } finally {
      setDeletingFile(null);
    }
  }

  // ── Agent linking ──
  async function handleLinkAgent() {
    if (!selected || !linkingAgent || linkingSaving) return;
    setLinkingSaving(true);
    setActionError(null);
    try {
      await api.linkAgentToKB(selected.name, linkingAgent);
      setLinkingAgent('');
      await refreshSelected(selected.id);
    } catch (err: unknown) {
      setActionError(err instanceof Error ? err.message : 'Failed to link agent');
    } finally {
      setLinkingSaving(false);
    }
  }

  async function handleUnlinkAgent(agentName: string) {
    if (!selected) return;
    setActionError(null);
    try {
      await api.unlinkAgentFromKB(selected.name, agentName);
      await refreshSelected(selected.id);
    } catch (err: unknown) {
      setActionError(err instanceof Error ? err.message : 'Failed to unlink agent');
    }
  }

  // ── Helpers ──
  function getModelName(modelId?: string): string {
    if (!modelId) return '--';
    const model = modelsMap.get(modelId);
    return model?.name ?? modelId;
  }

  const isEdit = editTarget !== null;

  // ── Table columns ──
  const columns = [
    {
      key: 'name',
      header: 'Name',
      render: (row: KnowledgeBase) => (
        <span className="text-[13px] text-brand-light font-medium">{row.name}</span>
      ),
    },
    {
      key: 'description',
      header: 'Description',
      render: (row: KnowledgeBase) => (
        <span className="text-[13px] text-brand-shade3 truncate max-w-[200px] inline-block">
          {row.description || '--'}
        </span>
      ),
    },
    {
      key: 'embedding_model_id',
      header: 'Embedding Model',
      render: (row: KnowledgeBase) => (
        <span className="text-[13px] text-brand-shade3">{getModelName(row.embedding_model_id)}</span>
      ),
    },
    {
      key: 'file_count',
      header: 'Files',
      render: (row: KnowledgeBase) => (
        <span className="text-[13px] text-brand-shade3">{row.file_count}</span>
      ),
    },
    {
      key: 'linked_agents',
      header: 'Agents',
      render: (row: KnowledgeBase) => (
        <span className="text-[13px] text-brand-shade3">{row.linked_agents.length}</span>
      ),
    },
    {
      key: 'created_at',
      header: 'Created',
      render: (row: KnowledgeBase) => (
        <span className="text-[11px] text-brand-shade3 font-mono">
          {new Date(row.created_at).toLocaleDateString()}
        </span>
      ),
    },
  ];

  // ── File table columns for detail panel ──
  const fileColumns = [
    {
      key: 'name',
      header: 'Name',
      render: (row: KnowledgeFile) => (
        <span className="text-[13px] text-brand-light font-medium truncate max-w-[140px] inline-block">
          {row.name}
        </span>
      ),
    },
    {
      key: 'type',
      header: 'Type',
      render: (row: KnowledgeFile) => (
        <span className="text-[11px] text-brand-shade3 font-mono uppercase">{row.type}</span>
      ),
    },
    {
      key: 'size',
      header: 'Size',
      render: (row: KnowledgeFile) => (
        <span className="text-[11px] text-brand-shade3 font-mono">{row.size}</span>
      ),
    },
    {
      key: 'status',
      header: 'Status',
      render: (row: KnowledgeFile) => formatFileStatus(row.status, row.error),
    },
    {
      key: 'chunk_count',
      header: 'Chunks',
      render: (row: KnowledgeFile) => (
        <span className="text-[11px] text-brand-shade3">{row.chunk_count ?? '--'}</span>
      ),
    },
    {
      key: 'actions',
      header: '',
      render: (row: KnowledgeFile) => (
        <div className="flex items-center gap-1.5">
          <button
            onClick={(e) => {
              e.stopPropagation();
              if (row.id) handleDeleteFile(row.id);
            }}
            disabled={deletingFile === row.id}
            className="px-2 py-0.5 text-[11px] text-red-400 border border-red-500/30 rounded-btn hover:bg-red-500/10 transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
          >
            {deletingFile === row.id ? '...' : 'Delete'}
          </button>
        </div>
      ),
    },
  ];

  // ── Loading / error states ──
  if (loading) return <div className="text-brand-shade3">Loading knowledge bases...</div>;
  if (error) return <div className="text-red-400">Error: {error}</div>;

  return (
    <PageContainer>
      {/* ── Header ── */}
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold text-brand-light">Knowledge Bases</h1>
        <button
          onClick={openCreate}
          className="px-4 py-2 bg-brand-accent text-white rounded-btn text-sm font-medium hover:bg-brand-accent-hover transition-colors"
        >
          Create Knowledge Base
        </button>
      </div>

      {/* ── Data Table ── */}
      <div className="bg-brand-dark-alt rounded-card border border-brand-shade3/15">
        <DataTable
          columns={columns}
          data={knowledgeBases ?? []}
          keyField="id"
          onRowClick={handleSelect}
          activeKey={selected?.id}
          emptyMessage="No knowledge bases configured"
          emptyIcon={emptyIcons.knowledge}
          emptyAction={{ label: 'Create Knowledge Base', onClick: openCreate }}
        />
      </div>

      {/* ── Detail Panel ── */}
      <DetailPanel
        open={selected !== null}
        onClose={handleClosePanel}
        title={selected?.name ?? ''}
        width="w-[520px]"
        actions={
          selected ? (
            <>
              <button
                onClick={() => openEdit(selected)}
                className="flex-1 px-4 py-2 bg-brand-accent text-white rounded-btn text-sm font-medium hover:bg-brand-accent-hover transition-colors"
              >
                Edit
              </button>
              <button
                onClick={() => setDeleteTarget(selected)}
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
            {/* Action error */}
            {actionError && (
              <div className="mb-4 p-3 bg-red-500/10 border border-red-500/30 rounded-btn text-xs text-red-400">
                {actionError}
              </div>
            )}

            {/* ── General ── */}
            <DetailSection title="General">
              <DetailRow label="Name">{selected.name}</DetailRow>
              <DetailRow label="Description">
                <span className="text-xs text-brand-shade2">{selected.description || '--'}</span>
              </DetailRow>
              <DetailRow label="Embedding Model">{getModelName(selected.embedding_model_id)}</DetailRow>
              <DetailRow label="Created">{new Date(selected.created_at).toLocaleString()}</DetailRow>
              <DetailRow label="Updated">{new Date(selected.updated_at).toLocaleString()}</DetailRow>
            </DetailSection>

            {/* ── Files ── */}
            <DetailSection title={`Files (${selected.file_count})`}>
              {/* Upload area */}
              <div
                onDragOver={(e) => {
                  e.preventDefault();
                  setDragOver(true);
                }}
                onDragLeave={() => setDragOver(false)}
                onDrop={handleDrop}
                onClick={() => fileInputRef.current?.click()}
                className={[
                  'mb-3 rounded-card border-2 border-dashed p-4 text-center cursor-pointer transition-all duration-150',
                  dragOver
                    ? 'border-brand-accent bg-brand-accent/5'
                    : 'border-brand-shade3/30 hover:border-brand-shade3/50 hover:bg-brand-dark/50',
                ].join(' ')}
              >
                <div className="flex flex-col items-center gap-1.5">
                  {uploadIcon}
                  {uploading ? (
                    <p className="text-xs text-brand-shade2">Uploading...</p>
                  ) : (
                    <>
                      <p className="text-xs text-brand-shade2">
                        Drop files here or <span className="text-brand-accent">browse</span>
                      </p>
                      <p className="text-[10px] text-brand-shade3">TXT, MD, CSV, PDF, DOCX</p>
                    </>
                  )}
                </div>
                <input
                  ref={fileInputRef}
                  type="file"
                  accept={ACCEPTED_TYPES}
                  multiple
                  className="hidden"
                  onChange={(e) => handleUpload(e.target.files)}
                />
              </div>

              {/* File table */}
              {filesLoading ? (
                <div className="text-xs text-brand-shade3 py-3">Loading files...</div>
              ) : files.length > 0 ? (
                <div className="border border-brand-shade3/15 rounded-card overflow-hidden">
                  <DataTable
                    columns={fileColumns}
                    data={files}
                    keyField="id"
                    emptyMessage="No files"
                  />
                </div>
              ) : (
                <div className="text-xs text-brand-shade3 py-3 text-center">
                  No files uploaded yet
                </div>
              )}
            </DetailSection>

            {/* ── Linked Agents ── */}
            <DetailSection title={`Linked Agents (${selected.linked_agents.length})`}>
              {/* Current linked agents */}
              {selected.linked_agents.length > 0 ? (
                <div className="space-y-1.5 mb-3">
                  {selected.linked_agents.map((agentName) => {
                    const agent = agentsMap.get(agentName);
                    return (
                      <div
                        key={agentName}
                        className="flex items-center justify-between py-1.5 px-2.5 bg-brand-dark rounded-btn border border-brand-shade3/15"
                      >
                        <span className="text-xs text-brand-light font-medium">
                          {agent?.name ?? agentName}
                        </span>
                        <button
                          onClick={() => handleUnlinkAgent(agentName)}
                          className="px-2 py-0.5 text-[11px] text-red-400 border border-red-500/30 rounded-btn hover:bg-red-500/10 transition-colors"
                        >
                          Unlink
                        </button>
                      </div>
                    );
                  })}
                </div>
              ) : (
                <div className="text-xs text-brand-shade3 py-2 mb-3">No agents linked</div>
              )}

              {/* Link new agent */}
              {availableAgents.length > 0 && (
                <div className="flex items-center gap-2">
                  <select
                    value={linkingAgent}
                    onChange={(e) => setLinkingAgent(e.target.value)}
                    className="flex-1 px-2.5 py-1.5 bg-brand-dark border border-brand-shade3/30 rounded-card text-xs text-brand-light focus:outline-none focus:border-brand-accent transition-colors"
                  >
                    <option value="">Select agent...</option>
                    {availableAgents.map((a: AgentInfo) => (
                      <option key={a.name} value={a.name}>
                        {a.name}
                      </option>
                    ))}
                  </select>
                  <button
                    onClick={handleLinkAgent}
                    disabled={!linkingAgent || linkingSaving}
                    className="px-3 py-1.5 bg-brand-accent text-white rounded-btn text-xs font-medium hover:bg-brand-accent-hover disabled:opacity-50 transition-colors"
                  >
                    {linkingSaving ? '...' : 'Link'}
                  </button>
                </div>
              )}
            </DetailSection>
          </>
        )}
      </DetailPanel>

      {/* ── Create / Edit Form Modal ── */}
      <FormModal
        open={showForm}
        onClose={closeForm}
        title={isEdit ? 'Edit Knowledge Base' : 'Create Knowledge Base'}
        onSubmit={handleSubmit}
        submitLabel={isEdit ? 'Save Changes' : 'Create'}
        loading={saving}
      >
        <FormField
          label="Name"
          value={form.name}
          onChange={(v) => setForm({ ...form, name: v })}
          required
          disabled={isEdit}
          placeholder="support-docs"
          hint={isEdit ? 'Name cannot be changed.' : undefined}
        />
        <FormField
          label="Description"
          type="textarea"
          value={form.description ?? ''}
          onChange={(v) => setForm({ ...form, description: v })}
          placeholder="Customer support documentation and FAQ"
          rows={3}
        />
        <FormField
          label="Embedding Model"
          type="select"
          value={form.embedding_model_id}
          onChange={(v) => setForm({ ...form, embedding_model_id: v })}
          required
          options={[
            { value: '', label: '-- Select embedding model --' },
            ...embeddingModels.map((m: Model) => ({
              value: m.id,
              label: m.name,
            })),
          ]}
          hint={
            embeddingModels.length === 0
              ? 'No embedding models found. Add one in Models page first (kind = Embedding Model).'
              : undefined
          }
        />
      </FormModal>

      {/* ── Confirm Delete ── */}
      <ConfirmDialog
        open={deleteTarget !== null}
        onClose={() => setDeleteTarget(null)}
        onConfirm={handleDelete}
        title="Remove Knowledge Base"
        message={
          <>
            Remove knowledge base <strong className="text-brand-light">{deleteTarget?.name}</strong>?
            This will delete all files and unlink all agents.
          </>
        }
        confirmLabel="Remove"
        variant="danger"
      />
    </PageContainer>
  );
}
