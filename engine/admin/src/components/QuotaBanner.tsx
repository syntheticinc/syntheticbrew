import { useState, useEffect, useCallback } from 'react';
import { api } from '../api/client';
import type { UsageStatusData } from '../types';

const DISMISS_KEY = 'syntheticbrew_quota_banner_dismissed';

const METRICS: Array<{ key: keyof UsageStatusData; label: string; recover?: string }> = [
  { key: 'active_users', label: 'Monthly active users' },
  { key: 'schemas', label: 'Schemas', recover: 'schema' },
  { key: 'knowledge_documents', label: 'Knowledge documents', recover: 'document' },
  { key: 'turns', label: 'Turns' },
];

interface WorstMetric {
  label: string;
  pct: number;
  // Countable resources (schemas, documents) let an over-limit user
  // self-recover by deleting one; recover is the singular noun for that hint.
  recover?: string;
}

export default function QuotaBanner() {
  const [status, setStatus] = useState<UsageStatusData | null>(null);
  const [dismissed, setDismissed] = useState(false);

  useEffect(() => {
    // Reappear on page load (clear previous dismissal)
    sessionStorage.removeItem(DISMISS_KEY);

    api
      .getUsageStatus()
      .then(setStatus)
      .catch(() => {});
  }, []);

  const dismiss = useCallback(() => {
    setDismissed(true);
    sessionStorage.setItem(DISMISS_KEY, 'true');
  }, []);

  // Plan management lives on the cloud dashboard; self-hosted deployments
  // configure limits via the engine and get no external link here.
  const landingUrl = import.meta.env.VITE_LANDING_URL as string | undefined;
  const upgradeUrl =
    import.meta.env.VITE_AUTH_MODE === 'external' && landingUrl ? `${landingUrl}/billing` : null;

  // Find the worst metric (unlimited metrics never warn)
  const worstMetric = METRICS.reduce<WorstMetric | null>((acc, { key, label, recover }) => {
    const m = status?.[key];
    if (!m || m.limit === null || m.limit <= 0) return acc;
    const pct = (m.used / m.limit) * 100;
    if (!acc || pct > acc.pct) return { label, pct, recover };
    return acc;
  }, null);

  if (dismissed || !worstMetric || worstMetric.pct < 80) {
    return null;
  }

  // Never block the UI: over-limit is a non-blocking red banner that tells the
  // user how to self-recover (delete a resource) — walling the whole dashboard
  // behind an "upgrade" modal traps users who just need to delete a schema to
  // get back under their limit. 80-94% amber, 95%+ red.
  const pct = worstMetric.pct;
  const isOver = pct > 100;
  const isRed = pct >= 95;
  const bgClass = isRed ? 'bg-red-500/10 border-red-500/30' : 'bg-amber-500/10 border-amber-500/30';
  const textClass = isRed ? 'text-red-400' : 'text-amber-400';

  let message: string;
  if (isOver && worstMetric.recover) {
    message = `Over your ${worstMetric.label} limit (${pct.toFixed(0)}%). Remove a ${worstMetric.recover} to get back under your limit${upgradeUrl ? ', or upgrade' : ''}.`;
  } else if (isOver) {
    message = `Over your ${worstMetric.label} limit (${pct.toFixed(0)}%). ${upgradeUrl ? 'Upgrade for a higher limit' : 'Raise the configured limit'}.`;
  } else if (isRed) {
    message = `Almost at limit — ${worstMetric.label} at ${pct.toFixed(0)}%.`;
  } else {
    message = `You've used ${pct.toFixed(0)}% of your ${worstMetric.label} limit this month.`;
  }

  return (
    <div className={`flex items-center justify-between px-4 py-2 border-b ${bgClass} shrink-0`}>
      <div className="flex items-center gap-2">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className={textClass}>
          <path d="M10.29 3.86L1.82 18a2 2 0 001.71 3h16.94a2 2 0 001.71-3L13.71 3.86a2 2 0 00-3.42 0z" />
          <line x1="12" y1="9" x2="12" y2="13" />
          <line x1="12" y1="17" x2="12.01" y2="17" />
        </svg>
        <span className={`text-xs font-mono ${textClass}`}>{message}</span>
      </div>
      <div className="flex items-center gap-2 shrink-0">
        {upgradeUrl && (
          <a
            href={upgradeUrl}
            target="_blank"
            rel="noreferrer"
            className={`px-3 py-1 rounded-btn text-xs font-medium font-mono transition-colors ${
              isRed
                ? 'bg-red-500/20 text-red-400 hover:bg-red-500/30'
                : 'bg-amber-500/20 text-amber-400 hover:bg-amber-500/30'
            }`}
          >
            Upgrade
          </a>
        )}
        <button
          onClick={dismiss}
          className="p-1 text-brand-shade3 hover:text-brand-light transition-colors"
          title="Dismiss"
        >
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
            <path d="M18 6L6 18M6 6l12 12" />
          </svg>
        </button>
      </div>
    </div>
  );
}
