import { useCallback, useEffect, useState } from 'react';
import { api } from '../api/client';
import type { CircuitBreakerState } from '../types';

const POLL_INTERVAL_MS = 5000;

function formatRelative(iso?: string | null): string {
  if (!iso) return '-';
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return '-';
  const delta = Math.max(0, Date.now() - then);
  const sec = Math.floor(delta / 1000);
  if (sec < 60) return `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  return `${Math.floor(hr / 24)}d ago`;
}

function StateBadge({ state }: { state: CircuitBreakerState['state'] }) {
  const map: Record<CircuitBreakerState['state'], string> = {
    closed: 'bg-status-active/15 text-status-active',
    open: 'bg-red-500/15 text-red-400',
    half_open: 'bg-amber-500/15 text-amber-400',
  };
  return (
    <span className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ${map[state]}`}>
      {state.replace('_', ' ')}
    </span>
  );
}

export default function ResiliencePage() {
  const [breakers, setBreakers] = useState<CircuitBreakerState[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [toast, setToast] = useState<string | null>(null);

  const fetchAll = useCallback(async () => {
    try {
      const cb = await api.listCircuitBreakers();
      setBreakers(cb);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchAll();
    const id = setInterval(fetchAll, POLL_INTERVAL_MS);
    return () => clearInterval(id);
  }, [fetchAll]);

  useEffect(() => {
    if (!toast) return;
    const id = setTimeout(() => setToast(null), 4000);
    return () => clearTimeout(id);
  }, [toast]);

  async function handleReset(name: string) {
    try {
      await api.resetCircuitBreaker(name);
      setToast(`Circuit breaker ${name} reset`);
      fetchAll();
    } catch (e) {
      setToast(e instanceof Error ? e.message : String(e));
    }
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-2xl font-bold text-brand-light">Resilience</h1>
          <p className="text-sm text-brand-shade3 mt-1">
            Circuit breaker observability for MCP servers. Polls every {POLL_INTERVAL_MS / 1000}s.
          </p>
        </div>
        <button
          onClick={fetchAll}
          className="px-4 py-2 text-sm text-brand-shade2 border border-brand-shade3/30 rounded-btn hover:bg-brand-dark-alt hover:text-brand-light transition-colors"
        >
          Refresh
        </button>
      </div>

      {error && (
        <div className="mb-4 p-3 bg-red-500/10 border border-red-500/30 rounded-btn text-sm text-red-400">
          Error: {error}
        </div>
      )}

      {toast && (
        <div className="mb-4 p-3 bg-brand-accent/10 border border-brand-accent/30 rounded-btn text-sm text-white">
          {toast}
        </div>
      )}

      <Section title="Circuit Breakers" hint="One breaker per MCP server. Opens after consecutive failures; half-open probes recovery.">
        {loading && breakers.length === 0 ? (
          <Loading />
        ) : breakers.length === 0 ? (
          <Empty message="No circuit breakers registered." />
        ) : (
          <table className="w-full text-sm">
            <thead className="text-left text-xs text-brand-shade3 border-b border-brand-shade3/15">
              <tr>
                <th className="px-4 py-2 font-medium">MCP Server</th>
                <th className="px-4 py-2 font-medium">State</th>
                <th className="px-4 py-2 font-medium">Failures</th>
                <th className="px-4 py-2 font-medium">Last failure</th>
                <th className="px-4 py-2 font-medium text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {breakers.map((b) => (
                <tr key={b.name} className="border-b border-brand-shade3/10 last:border-0">
                  <td className="px-4 py-3 text-brand-light font-mono text-xs">{b.name}</td>
                  <td className="px-4 py-3"><StateBadge state={b.state} /></td>
                  <td className="px-4 py-3 text-brand-shade2">{b.failure_count}</td>
                  <td className="px-4 py-3 text-brand-shade3 text-xs">{formatRelative(b.last_failure)}</td>
                  <td className="px-4 py-3 text-right">
                    {b.state !== 'closed' && (
                      <button
                        onClick={() => handleReset(b.name)}
                        className="px-3 py-1 text-xs text-brand-shade2 border border-brand-shade3/30 rounded-btn hover:bg-brand-dark hover:text-brand-light transition-colors"
                      >
                        Reset
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Section>
    </div>
  );
}

function Section({ title, hint, children }: { title: string; hint?: string; children: React.ReactNode }) {
  return (
    <div className="mb-8 bg-brand-dark-alt rounded-card border border-brand-shade3/15">
      <div className="px-4 py-3 border-b border-brand-shade3/15">
        <h2 className="text-sm font-semibold text-brand-light">{title}</h2>
        {hint && <p className="text-xs text-brand-shade3 mt-0.5">{hint}</p>}
      </div>
      {children}
    </div>
  );
}

function Loading() {
  return <div className="px-4 py-6 text-xs text-brand-shade3">Loading…</div>;
}

function Empty({ message }: { message: string }) {
  return <div className="px-4 py-6 text-xs text-brand-shade3">{message}</div>;
}
