import { useCallback, useEffect, useRef, useState } from 'react';
import { api } from '../api/client';
import { useApi } from '../hooks/useApi';
import DataTable from '../components/DataTable';
import { emptyIcons } from '../components/EmptyState';
import StatusBadge from '../components/StatusBadge';
import DetailPanel, { DetailRow, DetailSection } from '../components/DetailPanel';
import ConfirmDialog from '../components/ConfirmDialog';
import PromptDialog from '../components/PromptDialog';
import TaskCreateForm from '../components/TaskCreateForm';
import type { AgentInfo, TaskResponse, TaskDetailResponse, CreateTaskRequest } from '../types';

const STATUS_OPTIONS = ['', 'draft', 'pending', 'approved', 'in_progress', 'completed', 'failed', 'cancelled', 'needs_input', 'escalated'];
const SOURCE_OPTIONS = ['', 'agent', 'cron', 'webhook', 'api', 'dashboard'];
const PER_PAGE = 20;
const TERMINAL_STATUSES = new Set(['completed', 'failed', 'cancelled']);
// Background refresh cadence. Keeps the list in sync with agent-driven changes.
const AUTO_REFRESH_INTERVAL_MS = 5_000;

const PRIORITY_LABELS: { label: string; className: string }[] = [
  { label: 'Normal', className: 'text-brand-shade2 bg-brand-dark' },
  { label: 'High', className: 'text-amber-200 bg-amber-500/15' },
  { label: 'Critical', className: 'text-red-300 bg-red-500/15' },
];

function PriorityBadge({ priority }: { priority: number }) {
  const idx = priority >= 0 && priority < PRIORITY_LABELS.length ? priority : 0;
  const meta = PRIORITY_LABELS[idx] ?? PRIORITY_LABELS[0] ?? { label: 'Normal', className: '' };
  return (
    <span className={`text-xs px-2 py-0.5 rounded ${meta.className}`}>{meta.label}</span>
  );
}

type PromptMode = null | 'complete' | 'fail';

export default function TasksPage() {
  const [filters, setFilters] = useState<Record<string, string>>({});
  const [page, setPage] = useState(1);
  const { data: paginatedData, loading, error, refetch } = useApi(
    () => {
      const params: Record<string, string> = {
        page: String(page),
        per_page: String(PER_PAGE),
      };
      for (const [k, v] of Object.entries(filters)) {
        if (v) params[k] = v;
      }
      return api.listTasksPaginated(params);
    },
    [JSON.stringify(filters), page],
  );

  const { data: agents } = useApi(() => api.listAgents(), []);

  const tasks = paginatedData?.data ?? [];
  const total = paginatedData?.total ?? 0;
  const totalPages = paginatedData?.total_pages ?? 0;
  const agentList: AgentInfo[] = agents ?? [];

  const [selectedTask, setSelectedTask] = useState<TaskDetailResponse | null>(null);
  const [loadingDetail, setLoadingDetail] = useState(false);
  const [subtasks, setSubtasks] = useState<TaskResponse[]>([]);
  const [loadingSubtasks, setLoadingSubtasks] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [busyAction, setBusyAction] = useState<string | null>(null);

  const [showCreate, setShowCreate] = useState(false);
  const [showAddSubtask, setShowAddSubtask] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);

  const [confirmCancel, setConfirmCancel] = useState<{ open: boolean; taskId: string | null; cascadeCount: number }>(
    { open: false, taskId: null, cascadeCount: 0 },
  );
  const [promptMode, setPromptMode] = useState<PromptMode>(null);

  // AbortController for subtasks fetch to avoid race conditions.
  const subtasksAbortRef = useRef<AbortController | null>(null);

  const loadDetail = useCallback(async (id: string, silent = false) => {
    if (!silent) {
      setLoadingDetail(true);
      setActionError(null);
    }
    try {
      const detail = await api.getTask(id);
      // Only update state if data actually changed — prevents detail panel
      // flicker on background auto-refresh when nothing changed.
      setSelectedTask((prev) => {
        if (prev && JSON.stringify(prev) === JSON.stringify(detail)) return prev;
        return detail;
      });
    } catch (e) {
      if (!silent) {
        setActionError(e instanceof Error ? e.message : String(e));
      }
    } finally {
      if (!silent) setLoadingDetail(false);
    }
  }, []);

  const loadSubtasks = useCallback(async (id: string) => {
    // Cancel previous fetch before starting a new one.
    if (subtasksAbortRef.current) {
      subtasksAbortRef.current.abort();
    }
    const controller = new AbortController();
    subtasksAbortRef.current = controller;
    setLoadingSubtasks(true);
    try {
      const subs = await api.listSubtasks(id);
      // If a newer fetch has started, discard this result.
      if (controller.signal.aborted) return;
      setSubtasks(subs);
    } catch (e) {
      if (!controller.signal.aborted) {
        setSubtasks([]);
      }
    } finally {
      if (!controller.signal.aborted) {
        setLoadingSubtasks(false);
      }
    }
  }, []);

  useEffect(() => {
    if (selectedTask) {
      loadSubtasks(selectedTask.id);
    } else {
      setSubtasks([]);
      if (subtasksAbortRef.current) {
        subtasksAbortRef.current.abort();
      }
    }
  }, [selectedTask, loadSubtasks]);

  // Cleanup any pending fetch on unmount.
  useEffect(() => {
    return () => {
      if (subtasksAbortRef.current) {
        subtasksAbortRef.current.abort();
      }
    };
  }, []);

  // Auto-refresh: poll the task list while the page is visible.
  // Pauses when the tab is hidden to avoid wasted calls on backgrounded tabs.
  // Also skips ticks while a user action is in flight so a mid-request refetch
  // cannot race with a POST/DELETE we just issued.
  useEffect(() => {
    let cancelled = false;
    const tick = () => {
      if (cancelled) return;
      if (document.visibilityState !== 'visible') return;
      if (busyAction) return;
      refetch();
      if (selectedTask) {
        loadDetail(selectedTask.id, true);
      }
    };
    const timer = window.setInterval(tick, AUTO_REFRESH_INTERVAL_MS);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
    // refetch and loadDetail are stable refs; rebinding on selectedTask change
    // keeps the interval pointing at the currently-open task.
  }, [refetch, loadDetail, selectedTask, busyAction]);

  async function handleRowClick(row: TaskResponse) {
    await loadDetail(row.id);
  }

  async function runAction(label: string, fn: () => Promise<void>) {
    if (busyAction) return; // prevent double-click / overlapping actions
    setActionError(null);
    setBusyAction(label);
    try {
      await fn();
      if (selectedTask) {
        await loadDetail(selectedTask.id);
      }
      refetch();
    } catch (e) {
      setActionError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusyAction(null);
    }
  }

  async function handleApprove(id: string) {
    await runAction('approve', () => api.approveTask(id));
  }

  async function handleStart(id: string) {
    await runAction('start', () => api.startTask(id));
  }

  async function handleComplete(result: string) {
    if (!selectedTask) return;
    const taskId = selectedTask.id;
    setPromptMode(null);
    await runAction('complete', () => api.completeTask(taskId, result || undefined));
  }

  async function handleFail(reason: string) {
    if (!selectedTask) return;
    const taskId = selectedTask.id;
    setPromptMode(null);
    await runAction('fail', () => api.failTask(taskId, reason));
  }

  async function openCancelConfirm(id: string) {
    // Count non-terminal children for cascade warning.
    let cascadeCount = 0;
    try {
      const subs = await api.listSubtasks(id);
      cascadeCount = subs.filter((s) => !TERMINAL_STATUSES.has(s.status)).length;
    } catch {
      // best-effort: show confirm without cascade info
    }
    setConfirmCancel({ open: true, taskId: id, cascadeCount });
  }

  async function confirmCancelTask() {
    const id = confirmCancel.taskId;
    setConfirmCancel({ open: false, taskId: null, cascadeCount: 0 });
    if (!id) return;
    await runAction('cancel', async () => {
      await api.cancelTask(id);
      if (selectedTask?.id === id) {
        // Keep panel open — it will refresh with cancelled status.
      }
    });
  }

  async function handleSetPriority(id: string, priority: number) {
    await runAction('priority', () => api.setTaskPriority(id, priority));
  }

  async function handleCreateTask(data: CreateTaskRequest) {
    setCreateError(null);
    setCreating(true);
    try {
      const result = await api.createTask(data);
      setShowCreate(false);
      setShowAddSubtask(false);
      refetch();
      // If this was a subtask, refresh subtasks for the open parent.
      if (data.parent_task_id && selectedTask?.id === data.parent_task_id) {
        await loadSubtasks(data.parent_task_id);
      } else if (result.task_id) {
        // Auto-open the newly created top-level task.
        await loadDetail(result.task_id);
      }
    } catch (e) {
      setCreateError(e instanceof Error ? e.message : String(e));
    } finally {
      setCreating(false);
    }
  }

  const columns = [
    {
      key: 'id',
      header: 'ID',
      className: 'w-24',
      render: (row: TaskResponse) => (
        <span className="font-mono text-xs text-brand-shade3">#{row.id.slice(0, 8)}</span>
      ),
    },
    {
      key: 'title',
      header: 'Title',
      render: (row: TaskResponse) => (
        <span>
          {row.parent_task_id && <span className="text-brand-shade3 mr-1">↳</span>}
          {row.title}
        </span>
      ),
    },
    { key: 'agent_name', header: 'Agent' },
    {
      key: 'status',
      header: 'Status',
      render: (row: TaskResponse) => <StatusBadge status={row.status} />,
    },
    {
      key: 'priority',
      header: 'Priority',
      render: (row: TaskResponse) => <PriorityBadge priority={row.priority ?? 0} />,
    },
    {
      key: 'source',
      header: 'Source',
      render: (row: TaskResponse) => (
        <span className="text-xs text-brand-shade3 bg-brand-dark px-2 py-0.5 rounded">{row.source}</span>
      ),
    },
    {
      key: 'created_at',
      header: 'Created',
      render: (row: TaskResponse) => (
        <span className="text-xs text-brand-shade3">
          {new Date(row.created_at).toLocaleString()}
        </span>
      ),
    },
  ];

  const canApprove = selectedTask?.status === 'draft';
  const canStart = selectedTask ? ['approved', 'pending'].includes(selectedTask.status) : false;
  const canCompleteOrFail = selectedTask?.status === 'in_progress';
  const canCancel = selectedTask && !TERMINAL_STATUSES.has(selectedTask.status);
  const canAddSubtask = selectedTask && !TERMINAL_STATUSES.has(selectedTask.status);

  function actionClass(variant: 'primary' | 'success' | 'warning' | 'danger'): string {
    const base = 'px-3 py-2 text-sm text-white rounded-btn font-medium transition-opacity disabled:opacity-50 disabled:cursor-wait';
    switch (variant) {
      case 'success':
        return `${base} bg-emerald-600 hover:bg-emerald-700`;
      case 'warning':
        return `${base} bg-amber-600 hover:bg-amber-700`;
      case 'danger':
        return `${base} bg-red-600 hover:bg-red-700`;
      default:
        return `${base} bg-brand-accent hover:opacity-90`;
    }
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold text-brand-light">Tasks</h1>
        <div className="flex gap-2">
          <button
            onClick={() => { setCreateError(null); setShowCreate(true); }}
            className="px-4 py-2 text-sm text-white bg-brand-accent rounded-btn hover:opacity-90 transition-opacity font-medium"
          >
            + New task
          </button>
          <button
            onClick={refetch}
            className="px-4 py-2 text-sm text-brand-shade2 border border-brand-shade3/30 rounded-btn hover:bg-brand-dark-alt hover:text-brand-light transition-colors"
          >
            Refresh
          </button>
        </div>
      </div>

      {/* Filters */}
      <div className="flex gap-3 mb-4">
        <select
          value={filters['status'] ?? ''}
          onChange={(e) => { setFilters({ ...filters, status: e.target.value }); setPage(1); }}
          className="px-3 py-2 bg-brand-dark-alt border border-brand-shade3/50 rounded-card text-sm text-brand-light focus:outline-none focus:border-brand-accent"
        >
          <option value="">All statuses</option>
          {STATUS_OPTIONS.filter(Boolean).map((s) => (
            <option key={s} value={s}>
              {s.replace(/_/g, ' ')}
            </option>
          ))}
        </select>
        <select
          value={filters['source'] ?? ''}
          onChange={(e) => { setFilters({ ...filters, source: e.target.value }); setPage(1); }}
          className="px-3 py-2 bg-brand-dark-alt border border-brand-shade3/50 rounded-card text-sm text-brand-light focus:outline-none focus:border-brand-accent"
        >
          <option value="">All sources</option>
          {SOURCE_OPTIONS.filter(Boolean).map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </select>
        <input
          type="text"
          placeholder="Agent name..."
          value={filters['agent_name'] ?? ''}
          onChange={(e) => { setFilters({ ...filters, agent_name: e.target.value }); setPage(1); }}
          className="px-3 py-2 bg-brand-dark-alt border border-brand-shade3/50 rounded-card text-sm text-brand-light focus:outline-none focus:border-brand-accent"
        />
      </div>

      {loading && <div className="text-brand-shade3">Loading tasks...</div>}
      {error && <div className="text-red-600">Error: {error}</div>}

      {!loading && !error && (
        <div className="bg-brand-dark-alt rounded-card border border-brand-shade3/15">
          <DataTable
            columns={columns}
            data={tasks}
            keyField="id"
            onRowClick={handleRowClick}
            activeKey={selectedTask?.id}
            emptyMessage="No tasks found."
            emptyIcon={emptyIcons.tasks}
          />
          {totalPages > 1 && (
            <div className="flex items-center justify-between px-4 py-3 border-t border-brand-shade3/15">
              <span className="text-sm text-brand-shade3">
                Showing {(page - 1) * PER_PAGE + 1}–{Math.min(page * PER_PAGE, total)} of {total} tasks
              </span>
              <div className="flex gap-1">
                <button
                  onClick={() => setPage((p) => Math.max(1, p - 1))}
                  disabled={page <= 1}
                  className="px-3 py-1 text-sm text-brand-shade2 border border-brand-shade3/30 rounded-btn hover:bg-brand-dark hover:text-brand-light transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
                >
                  &lt;
                </button>
                {Array.from({ length: totalPages }, (_, i) => i + 1)
                  .filter((p) => p === 1 || p === totalPages || Math.abs(p - page) <= 2)
                  .reduce<(number | '...')[]>((acc, p, idx, arr) => {
                    if (idx > 0 && p - (arr[idx - 1] ?? 0) > 1) acc.push('...');
                    acc.push(p);
                    return acc;
                  }, [])
                  .map((item, idx) =>
                    item === '...' ? (
                      <span key={`ellipsis-${idx}`} className="px-2 py-1 text-sm text-brand-shade3">...</span>
                    ) : (
                      <button
                        key={item}
                        onClick={() => setPage(item)}
                        className={`px-3 py-1 text-sm border rounded-btn transition-colors ${
                          item === page
                            ? 'bg-brand-accent text-white border-brand-accent'
                            : 'text-brand-shade2 border-brand-shade3/30 hover:bg-brand-dark hover:text-brand-light'
                        }`}
                      >
                        {item}
                      </button>
                    ),
                  )}
                <button
                  onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
                  disabled={page >= totalPages}
                  className="px-3 py-1 text-sm text-brand-shade2 border border-brand-shade3/30 rounded-btn hover:bg-brand-dark hover:text-brand-light transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
                >
                  &gt;
                </button>
              </div>
            </div>
          )}
        </div>
      )}

      {/* Detail Panel */}
      <DetailPanel
        open={selectedTask !== null}
        onClose={() => setSelectedTask(null)}
        title={selectedTask ? `Task #${selectedTask.id.slice(0, 8)}: ${selectedTask.title}` : 'Task Detail'}
        actions={
          selectedTask ? (
            <div className="flex flex-wrap gap-2">
              {canApprove && (
                <button
                  onClick={() => handleApprove(selectedTask.id)}
                  disabled={!!busyAction}
                  className={actionClass('success')}
                >
                  {busyAction === 'approve' ? 'Approving...' : 'Approve'}
                </button>
              )}
              {canStart && (
                <button
                  onClick={() => handleStart(selectedTask.id)}
                  disabled={!!busyAction}
                  className={actionClass('primary')}
                >
                  {busyAction === 'start' ? 'Starting...' : 'Start'}
                </button>
              )}
              {canCompleteOrFail && (
                <>
                  <button
                    onClick={() => setPromptMode('complete')}
                    disabled={!!busyAction}
                    className={actionClass('success')}
                  >
                    {busyAction === 'complete' ? 'Completing...' : 'Complete'}
                  </button>
                  <button
                    onClick={() => setPromptMode('fail')}
                    disabled={!!busyAction}
                    className={actionClass('warning')}
                  >
                    {busyAction === 'fail' ? 'Failing...' : 'Fail'}
                  </button>
                </>
              )}
              {canAddSubtask && (
                <button
                  onClick={() => { setCreateError(null); setShowAddSubtask(true); }}
                  disabled={!!busyAction}
                  className="px-3 py-2 text-sm text-brand-shade2 border border-brand-shade3/30 rounded-btn hover:bg-brand-dark hover:text-brand-light transition-colors font-medium disabled:opacity-50"
                >
                  + Subtask
                </button>
              )}
              {canCancel && (
                <button
                  onClick={() => openCancelConfirm(selectedTask.id)}
                  disabled={!!busyAction}
                  className={actionClass('danger')}
                >
                  {busyAction === 'cancel' ? 'Cancelling...' : 'Cancel'}
                </button>
              )}
            </div>
          ) : undefined
        }
      >
        {loadingDetail ? (
          <div className="text-brand-shade3 text-sm">Loading...</div>
        ) : selectedTask ? (
          <>
            {actionError && (
              <div className="mb-3 p-2 text-xs text-red-300 bg-red-500/10 border border-red-500/30 rounded-btn">
                {actionError}
                <button
                  onClick={() => setActionError(null)}
                  className="float-right text-red-300 hover:text-red-100"
                >
                  ×
                </button>
              </div>
            )}
            <DetailSection title="Overview">
              <DetailRow label="Status"><StatusBadge status={selectedTask.status} /></DetailRow>
              <DetailRow label="Agent">{selectedTask.agent_name}</DetailRow>
              <DetailRow label="Source">
                <span className="text-xs text-brand-shade3 bg-brand-dark px-2 py-0.5 rounded">{selectedTask.source}</span>
              </DetailRow>
              <DetailRow label="Mode">{selectedTask.mode}</DetailRow>
              <DetailRow label="Priority">
                <div className="flex items-center gap-2">
                  <PriorityBadge priority={selectedTask.priority ?? 0} />
                  <select
                    value={String(selectedTask.priority ?? 0)}
                    onChange={(e) => handleSetPriority(selectedTask.id, Number(e.target.value))}
                    disabled={!!busyAction}
                    className="px-2 py-0.5 text-xs bg-brand-dark border border-brand-shade3/30 rounded text-brand-light focus:outline-none focus:border-brand-accent disabled:opacity-50"
                  >
                    <option value="0">Normal</option>
                    <option value="1">High</option>
                    <option value="2">Critical</option>
                  </select>
                  {busyAction === 'priority' && <span className="text-xs text-brand-shade3">saving...</span>}
                </div>
              </DetailRow>
              {selectedTask.parent_task_id && (
                <DetailRow label="Parent">
                  <button
                    onClick={() => loadDetail(selectedTask.parent_task_id!)}
                    className="text-brand-accent hover:underline font-mono text-xs"
                    title={selectedTask.parent_task_id}
                  >
                    #{selectedTask.parent_task_id.slice(0, 8)}
                  </button>
                </DetailRow>
              )}
              {selectedTask.assigned_agent_id && (
                <DetailRow label="Assigned agent">{selectedTask.assigned_agent_id}</DetailRow>
              )}
            </DetailSection>

            {selectedTask.description && (
              <DetailSection title="Description">
                <p className="text-sm text-brand-shade2 whitespace-pre-wrap">{selectedTask.description}</p>
              </DetailSection>
            )}

            {selectedTask.acceptance_criteria && selectedTask.acceptance_criteria.length > 0 && (
              <DetailSection title="Acceptance criteria">
                <ul className="space-y-1">
                  {selectedTask.acceptance_criteria.map((ac, i) => (
                    <li key={i} className="text-sm text-brand-shade2 flex gap-2">
                      <span className="text-brand-shade3">•</span>
                      <span>{ac}</span>
                    </li>
                  ))}
                </ul>
              </DetailSection>
            )}

            {selectedTask.blocked_by && selectedTask.blocked_by.length > 0 && (
              <DetailSection title="Blocked by">
                <div className="flex flex-wrap gap-2">
                  {selectedTask.blocked_by.map((id) => (
                    <button
                      key={id}
                      onClick={() => loadDetail(id)}
                      className="px-2 py-0.5 text-xs font-mono text-brand-shade2 bg-brand-dark border border-brand-shade3/30 rounded hover:bg-brand-dark-alt hover:text-brand-light transition-colors"
                      title={id}
                    >
                      #{id.slice(0, 8)}
                    </button>
                  ))}
                </div>
              </DetailSection>
            )}

            <DetailSection title="Subtasks">
              {loadingSubtasks ? (
                <div className="text-xs text-brand-shade3">Loading subtasks...</div>
              ) : subtasks.length === 0 ? (
                <div className="text-xs text-brand-shade3">No subtasks</div>
              ) : (
                <div className="space-y-1">
                  {subtasks.map((sub) => (
                    <button
                      key={sub.id}
                      onClick={() => loadDetail(sub.id)}
                      className="w-full text-left px-3 py-2 bg-brand-dark rounded-btn border border-brand-shade3/15 hover:border-brand-accent/40 transition-colors flex items-center justify-between gap-2"
                    >
                      <span className="text-sm text-brand-light truncate">
                        <span className="font-mono text-xs text-brand-shade3" title={sub.id}>#{sub.id.slice(0, 8)}</span> {sub.title}
                      </span>
                      <span className="flex items-center gap-2 flex-shrink-0">
                        <PriorityBadge priority={sub.priority ?? 0} />
                        <StatusBadge status={sub.status} />
                      </span>
                    </button>
                  ))}
                </div>
              )}
            </DetailSection>

            {selectedTask.result && (
              <DetailSection title="Result">
                <pre className="p-3 bg-brand-dark rounded-btn text-xs text-brand-shade2 whitespace-pre-wrap max-h-48 overflow-y-auto border border-brand-shade3/30">
                  {selectedTask.result}
                </pre>
              </DetailSection>
            )}

            {selectedTask.error && (
              <DetailSection title="Error">
                <pre className="p-3 bg-red-500/10 rounded-btn text-xs text-red-400 whitespace-pre-wrap max-h-48 overflow-y-auto border border-red-500/30">
                  {selectedTask.error}
                </pre>
              </DetailSection>
            )}

            <DetailSection title="Timestamps">
              <DetailRow label="Created">{new Date(selectedTask.created_at).toLocaleString()}</DetailRow>
              {selectedTask.approved_at && (
                <DetailRow label="Approved">{new Date(selectedTask.approved_at).toLocaleString()}</DetailRow>
              )}
              {selectedTask.started_at && (
                <DetailRow label="Started">{new Date(selectedTask.started_at).toLocaleString()}</DetailRow>
              )}
              {selectedTask.completed_at && (
                <DetailRow label="Completed">{new Date(selectedTask.completed_at).toLocaleString()}</DetailRow>
              )}
            </DetailSection>
          </>
        ) : null}
      </DetailPanel>

      {/* Create top-level task */}
      <TaskCreateForm
        open={showCreate}
        onClose={() => setShowCreate(false)}
        onSubmit={handleCreateTask}
        agents={agentList}
        blockerCandidates={tasks}
        loading={creating}
        errorMessage={createError}
      />

      {/* Add subtask */}
      <TaskCreateForm
        open={showAddSubtask && !!selectedTask}
        onClose={() => setShowAddSubtask(false)}
        onSubmit={handleCreateTask}
        agents={agentList}
        parentTask={selectedTask}
        loading={creating}
        errorMessage={createError}
      />

      {/* Cancel confirmation */}
      <ConfirmDialog
        open={confirmCancel.open}
        onClose={() => setConfirmCancel({ open: false, taskId: null, cascadeCount: 0 })}
        onConfirm={confirmCancelTask}
        title="Cancel task?"
        variant="danger"
        confirmLabel="Cancel task"
        loading={busyAction === 'cancel'}
        message={
          <>
            <p>This cannot be undone.</p>
            {confirmCancel.cascadeCount > 0 && (
              <p className="mt-2 text-amber-300">
                Warning: {confirmCancel.cascadeCount} non-terminal child task{confirmCancel.cascadeCount > 1 ? 's' : ''} will also be cancelled.
              </p>
            )}
          </>
        }
      />

      {/* Complete prompt */}
      <PromptDialog
        open={promptMode === 'complete'}
        onClose={() => setPromptMode(null)}
        onSubmit={handleComplete}
        title="Complete task"
        label="Result (optional)"
        placeholder="What was accomplished?"
        multiline
        submitLabel="Complete"
        variant="default"
        loading={busyAction === 'complete'}
      />

      {/* Fail prompt */}
      <PromptDialog
        open={promptMode === 'fail'}
        onClose={() => setPromptMode(null)}
        onSubmit={handleFail}
        title="Fail task"
        label="Reason"
        placeholder="Why did this task fail?"
        required
        multiline
        submitLabel="Fail task"
        variant="warning"
        loading={busyAction === 'fail'}
      />
    </div>
  );
}
