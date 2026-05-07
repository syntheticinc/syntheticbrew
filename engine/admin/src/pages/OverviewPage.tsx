import { Link } from 'react-router-dom';
import { useEffect, useState } from 'react';
import {
  mockOverviewEvents,
  mockSessions,
  mockSchemas,
  getSchemaById,
} from '../mocks/schemas';
import { usePrototype } from '../hooks/usePrototype';
import { api } from '../api/client';
import PageContainer from '../components/PageContainer';
import type { SessionSummary, Schema, HealthResponse } from '../types';

function formatRelativeTime(iso: string) {
  const diff = Date.now() - new Date(iso).getTime();
  if (diff < 60_000) return `${Math.floor(diff / 1000)}s ago`;
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`;
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h ago`;
  return `${Math.floor(diff / 86_400_000)}d ago`;
}

function Stat({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <div className="bg-brand-dark-surface border border-brand-shade3/15 rounded-card px-5 py-4">
      <div className="text-[10px] uppercase tracking-[0.2em] text-brand-shade3 mb-2">{label}</div>
      <div className="text-2xl font-semibold text-brand-light leading-tight">{value}</div>
      {hint && <div className="text-[11px] text-brand-shade3 mt-1">{hint}</div>}
    </div>
  );
}

const eventColor: Record<string, string> = {
  trigger_fired: 'text-amber-300 border-amber-500/30',
  delegation: 'text-purple-300 border-purple-500/30',
  session_completed: 'text-emerald-300 border-emerald-500/30',
  agent_error: 'text-red-300 border-red-500/30',
  flow_entered: 'text-blue-300 border-blue-500/30',
};

// ── Prototype mode component — uses mock data ────────────────────────────────

function OverviewPrototype() {
  const activeSessions = mockSessions.filter((s) => s.status === 'active');
  const completedSessions = mockSessions.filter((s) => s.status === 'completed');
  const failedSessions = mockSessions.filter((s) => s.status === 'failed');
  const sessionsToday = mockSchemas.reduce((sum, s) => sum + s.sessionsToday, 0);
  // In V2, chat is a schema-level toggle — all mock schemas are treated as
  // chat-enabled for prototype display. Replaces the removed triggers stat.
  const chatEnabledSchemas = mockSchemas.length;
  const finishedTotal = completedSessions.length + failedSessions.length;
  const successRate =
    finishedTotal > 0
      ? Math.round((completedSessions.length / finishedTotal) * 100)
      : null;

  return (
    <>
      {/* Stats grid — derived from mock data */}
      <div className="grid grid-cols-4 gap-4 mb-6">
        <Stat
          label="Active Sessions"
          value={String(activeSessions.length)}
          hint={`${mockSchemas.length} schemas`}
        />
        <Stat
          label="Sessions Today"
          value={sessionsToday.toLocaleString()}
          hint="across all schemas"
        />
        <Stat
          label="Chat-enabled Schemas"
          value={`${chatEnabledSchemas} / ${mockSchemas.length}`}
          hint={`${mockSchemas.length - chatEnabledSchemas} disabled`}
        />
        <Stat
          label="Success Rate"
          value={successRate !== null ? `${successRate}%` : '—'}
          hint={`${completedSessions.length} ok · ${failedSessions.length} failed`}
        />
      </div>

      <div className="grid grid-cols-[1.4fr_1fr] gap-6">
        {/* Live sessions */}
        <div className="bg-brand-dark-surface border border-brand-shade3/15 rounded-card">
          <div className="flex items-center justify-between px-5 py-3 border-b border-brand-shade3/10">
            <h2 className="text-sm font-semibold text-brand-light">Live Sessions</h2>
            <span className="flex items-center gap-1.5 text-[11px] text-emerald-400">
              <span className="w-1.5 h-1.5 rounded-full bg-emerald-400 animate-pulse" />
              {activeSessions.length} active
            </span>
          </div>
          <div className="divide-y divide-brand-shade3/10">
            {activeSessions.length === 0 && (
              <div className="px-5 py-6 text-center text-[12px] text-brand-shade3">
                No live sessions right now. Create your first{' '}
                <Link to="/schemas" className="text-brand-accent hover:underline">
                  schema
                </Link>{' '}
                to get started.
              </div>
            )}
            {activeSessions.map((s) => {
              const schema = getSchemaById(s.schemaId);
              // Engine 1.1.0+: schema URLs are name-keyed; UUID-shaped
              // mock IDs (s.schemaId) are internal-only.
              const schemaHandle = schema?.name ?? s.schemaId;
              return (
                <Link
                  key={s.id}
                  to={`/schemas/${encodeURIComponent(schemaHandle)}?session=${s.id}`}
                  className="flex items-center gap-3 px-5 py-3 hover:bg-brand-shade3/5 transition-colors group"
                >
                  <span className="font-mono text-[11px] text-brand-shade3 shrink-0">{s.id}</span>
                  <div className="min-w-0 flex-1">
                    <div className="text-[13px] text-brand-light truncate">{s.title}</div>
                    <div className="text-[10px] text-brand-shade3 mt-0.5">
                      {schema?.name} · {s.participantAgentIds.length} agents · started{' '}
                      {formatRelativeTime(s.startedAt)}
                    </div>
                  </div>
                  <span className="text-[11px] text-brand-shade3 group-hover:text-brand-accent transition-colors shrink-0">
                    Debug →
                  </span>
                </Link>
              );
            })}
          </div>
        </div>

        {/* Events feed */}
        <div className="bg-brand-dark-surface border border-brand-shade3/15 rounded-card">
          <div className="px-5 py-3 border-b border-brand-shade3/10">
            <h2 className="text-sm font-semibold text-brand-light">Recent Events</h2>
          </div>
          <div className="divide-y divide-brand-shade3/10 max-h-[400px] overflow-y-auto">
            {mockOverviewEvents.map((e, idx) => (
              <div key={idx} className="px-5 py-2.5 hover:bg-brand-shade3/5 transition-colors">
                <div className="flex items-center gap-2 mb-1">
                  <span
                    className={`text-[9px] uppercase tracking-wider border rounded px-1.5 py-0.5 ${eventColor[e.kind] ?? 'text-brand-shade3 border-brand-shade3/30'}`}
                  >
                    {e.kind.replace('_', ' ')}
                  </span>
                  <span className="text-[10px] text-brand-shade3 font-mono ml-auto">
                    {formatRelativeTime(e.timestamp)}
                  </span>
                </div>
                <div className="text-[12px] text-brand-shade2 leading-snug">{e.summary}</div>
              </div>
            ))}
          </div>
        </div>
      </div>

      {/* Schemas quick access */}
      <div className="mt-6">
        <div className="flex items-center justify-between mb-3">
          <h2 className="text-sm font-semibold text-brand-light">Schemas</h2>
          <Link
            to="/schemas"
            className="text-[11px] text-brand-shade3 hover:text-brand-accent transition-colors"
          >
            View all →
          </Link>
        </div>
        <div className="grid grid-cols-3 gap-3">
          {mockSchemas.map((s) => (
            <Link
              key={s.id}
              to={`/schemas/${encodeURIComponent(s.name)}`}
              className="bg-brand-dark-surface border border-brand-shade3/15 rounded-card px-4 py-3 hover:border-brand-shade3/35 transition-colors"
            >
              <div className="text-[13px] font-semibold text-brand-light truncate">{s.name}</div>
              <div className="flex items-center gap-2 mt-2 text-[10px] text-brand-shade3">
                <span>{s.agentIds.length} agents</span>
                <span className="text-brand-shade3/40">·</span>
                <span>{s.sessionsToday} today</span>
                <span className="text-brand-shade3/40">·</span>
                <span className={s.activeSessions > 0 ? 'text-emerald-400' : 'text-brand-shade3'}>
                  {s.activeSessions > 0 ? `${s.activeSessions} active` : 'idle'}
                </span>
              </div>
            </Link>
          ))}
        </div>
      </div>
    </>
  );
}

// ── Production mode component — fetches real data ────────────────────────────

interface ProductionStats {
  activeSessions: SessionSummary[];
  completedCount: number;
  failedCount: number;
  schemas: Schema[];
  health: HealthResponse | null;
  loading: boolean;
  error: string | null;
}

function useProductionStats(): ProductionStats {
  const [activeSessions, setActiveSessions] = useState<SessionSummary[]>([]);
  const [completedCount, setCompletedCount] = useState(0);
  const [failedCount, setFailedCount] = useState(0);
  const [schemas, setSchemas] = useState<Schema[]>([]);
  const [health, setHealth] = useState<HealthResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;

    Promise.all([
      // Running sessions (SessionStatus uses 'running', not 'active')
      api.listSessions({ status: ['running'], per_page: 50 }),
      // Completed sessions — for success rate numerator
      api.listSessions({ status: ['completed'], per_page: 1 }),
      // Failed + timeout sessions — for success rate denominator
      api.listSessions({ status: ['failed', 'timeout'], per_page: 1 }),
      // Schemas — for quick-access grid and chat-enabled count
      api.listSchemas(),
      // Health — for system status badge
      api.health(),
    ])
      .then(([activeRes, completedRes, failedRes, schemasRes, healthRes]) => {
        if (cancelled) return;
        setActiveSessions(activeRes.sessions);
        setCompletedCount(completedRes.total);
        setFailedCount(failedRes.total);
        setSchemas(schemasRes);
        setHealth(healthRes);
        setLoading(false);
      })
      .catch((err: Error) => {
        if (cancelled) return;
        setError(err.message);
        setLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, []);

  return { activeSessions, completedCount, failedCount, schemas, health, loading, error };
}

function SystemBadge({ health }: { health: HealthResponse }) {
  const ok = health.status?.toLowerCase() === 'ok' || health.status?.toLowerCase() === 'healthy';
  return (
    <span
      className={`inline-flex items-center gap-1.5 text-[11px] px-2 py-0.5 rounded-full border ${ok ? 'text-emerald-400 border-emerald-500/30 bg-emerald-500/5' : 'text-amber-400 border-amber-500/30 bg-amber-500/5'}`}
    >
      <span className={`w-1.5 h-1.5 rounded-full ${ok ? 'bg-emerald-400' : 'bg-amber-400'}`} />
      System: {ok ? 'OK' : health.status}
    </span>
  );
}

function OverviewProduction() {
  const { activeSessions, completedCount, failedCount, schemas, health, loading, error } =
    useProductionStats();

  if (loading) {
    return (
      <div className="text-brand-shade3 text-sm py-8 text-center">Loading overview...</div>
    );
  }

  if (error) {
    return (
      <div className="text-red-400 text-sm py-4">
        Failed to load overview: {error}
      </div>
    );
  }

  const chatEnabledSchemas = schemas.filter((s) => s.chat_enabled).length;
  const finishedTotal = completedCount + failedCount;
  const successRate =
    finishedTotal > 0 ? Math.round((completedCount / finishedTotal) * 100) : null;

  return (
    <>
      {/* Stats grid — derived from real API data only, no fabricated metrics */}
      <div className="grid grid-cols-4 gap-4 mb-6">
        <Stat
          label="Active Sessions"
          value={String(activeSessions.length)}
          hint={`${schemas.length} schemas`}
        />
        {/*
          Sessions Today: no backend endpoint exposes a per-day session count.
          The sessions API returns paginated results without a "created today" sum.
          Showing "—" until backend adds this metric.
        */}
        <Stat label="Sessions Today" value="—" hint="no daily counter in API" />
        <Stat
          label="Chat-enabled Schemas"
          value={schemas.length > 0 ? `${chatEnabledSchemas} / ${schemas.length}` : '—'}
          hint={
            schemas.length > 0
              ? `${schemas.length - chatEnabledSchemas} disabled`
              : undefined
          }
        />
        <Stat
          label="Success Rate"
          value={successRate !== null ? `${successRate}%` : '—'}
          hint={
            finishedTotal > 0
              ? `${completedCount} ok · ${failedCount} failed`
              : 'no completed sessions yet'
          }
        />
      </div>

      <div className="grid grid-cols-[1.4fr_1fr] gap-6">
        {/* Live sessions */}
        <div className="bg-brand-dark-surface border border-brand-shade3/15 rounded-card">
          <div className="flex items-center justify-between px-5 py-3 border-b border-brand-shade3/10">
            <h2 className="text-sm font-semibold text-brand-light">Live Sessions</h2>
            <span className="flex items-center gap-1.5 text-[11px] text-emerald-400">
              <span className="w-1.5 h-1.5 rounded-full bg-emerald-400 animate-pulse" />
              {activeSessions.length} active
            </span>
          </div>
          <div className="divide-y divide-brand-shade3/10">
            {activeSessions.length === 0 && (
              <div className="px-5 py-6 text-center text-[12px] text-brand-shade3">
                No live sessions right now.
              </div>
            )}
            {activeSessions.map((s) => (
              <div
                key={s.session_id}
                className="flex items-center gap-3 px-5 py-3"
              >
                <span className="font-mono text-[11px] text-brand-shade3 shrink-0">
                  {s.session_id.slice(0, 8)}
                </span>
                <div className="min-w-0 flex-1">
                  <div className="text-[13px] text-brand-light truncate">{s.entry_agent}</div>
                  <div className="text-[10px] text-brand-shade3 mt-0.5">
                    started {formatRelativeTime(s.created_at)}
                  </div>
                </div>
              </div>
            ))}
          </div>
        </div>

        {/* Recent Events — no event stream in current backend; show empty state */}
        <div className="bg-brand-dark-surface border border-brand-shade3/15 rounded-card">
          <div className="px-5 py-3 border-b border-brand-shade3/10">
            <h2 className="text-sm font-semibold text-brand-light">Recent Events</h2>
          </div>
          <div className="px-5 py-6 text-center text-[12px] text-brand-shade3">
            Event stream not available yet.
            <br />
            <span className="text-brand-shade3/60">
              Use{' '}
              <Link to="/tasks" className="hover:text-brand-accent transition-colors">
                Tasks
              </Link>{' '}
              or{' '}
              <Link to="/audit" className="hover:text-brand-accent transition-colors">
                Audit Log
              </Link>{' '}
              for activity history.
            </span>
          </div>
        </div>
      </div>

      {/* System Health */}
      {health && (
        <div className="mt-6">
          <div className="flex items-center gap-3 mb-3">
            <h2 className="text-sm font-semibold text-brand-light">System Health</h2>
            <SystemBadge health={health} />
            {health.update_available && (
              <span className="text-[11px] text-amber-300 border border-amber-500/40 bg-amber-500/10 rounded-btn px-2 py-0.5">
                Update available: v{health.update_available}
              </span>
            )}
          </div>
          <div className="grid grid-cols-4 gap-4">
            <Stat label="Status" value={health.status ?? 'ok'} />
            <Stat label="Version" value={health.version || 'dev'} />
            <Stat label="Uptime" value={health.uptime || '—'} />
            <Stat label="Agents" value={String(health.agents_count ?? 0)} />
          </div>
        </div>
      )}

      {/* Schemas quick access */}
      {schemas.length > 0 && (
        <div className="mt-6">
          <div className="flex items-center justify-between mb-3">
            <h2 className="text-sm font-semibold text-brand-light">Schemas</h2>
            <Link
              to="/schemas"
              className="text-[11px] text-brand-shade3 hover:text-brand-accent transition-colors"
            >
              View all →
            </Link>
          </div>
          <div className="grid grid-cols-3 gap-3">
            {schemas.slice(0, 6).map((s) => (
              <Link
                key={s.id}
                to={`/schemas/${encodeURIComponent(s.name)}`}
                className="bg-brand-dark-surface border border-brand-shade3/15 rounded-card px-4 py-3 hover:border-brand-shade3/35 transition-colors"
              >
                <div className="text-[13px] font-semibold text-brand-light truncate">{s.name}</div>
                <div className="flex items-center gap-2 mt-2 text-[10px] text-brand-shade3">
                  <span>{s.agents_count} agents</span>
                </div>
              </Link>
            ))}
          </div>
        </div>
      )}
    </>
  );
}

// ── Top-level page — mode-aware ───────────────────────────────────────────────

export default function OverviewPage() {
  const { isPrototype } = usePrototype();

  return (
    <PageContainer>
      <div className="mb-8">
        <h1 className="text-2xl font-semibold text-brand-light">Overview</h1>
        <p className="text-sm text-brand-shade3 mt-1">
          Live picture of what your agents are doing right now.
        </p>
      </div>

      {isPrototype ? <OverviewPrototype /> : <OverviewProduction />}
    </PageContainer>
  );
}
