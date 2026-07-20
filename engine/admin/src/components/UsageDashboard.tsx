import { useState, useEffect } from 'react';
import { api } from '../api/client';
import type { UsageStatusData, UsageStatusMetric } from '../types';

const METRICS: Array<{ key: keyof UsageStatusData; label: string }> = [
  { key: 'active_users', label: 'Monthly active users' },
  { key: 'schemas', label: 'Schemas' },
  { key: 'knowledge_documents', label: 'Knowledge documents' },
  { key: 'turns', label: 'Turns' },
];

function getBarColor(pct: number): string {
  if (pct >= 100) return 'bg-red-500';
  if (pct >= 80) return 'bg-amber-500';
  return 'bg-brand-accent';
}

function getPctColor(pct: number): string {
  if (pct >= 100) return 'text-red-400';
  if (pct >= 80) return 'text-amber-400';
  return 'text-brand-shade3';
}

function UsageBar({ label, metric }: { label: string; metric: UsageStatusMetric }) {
  const pct =
    metric.limit !== null && metric.limit > 0
      ? Math.min(100, (metric.used / metric.limit) * 100)
      : 0;
  const barColor = getBarColor(pct);
  const limitLabel = metric.limit === null ? 'Unlimited' : metric.limit.toLocaleString();

  return (
    <div className="bg-brand-dark border border-brand-shade3/15 rounded-card p-4">
      <div className="flex items-center justify-between mb-2">
        <span className="text-sm font-medium text-brand-light font-mono">{label}</span>
        <span className="text-xs text-brand-shade3 font-mono">
          {metric.used.toLocaleString()} / {limitLabel}
        </span>
      </div>
      <div className="h-3 bg-brand-dark-alt rounded-full overflow-hidden">
        <div
          className={`h-full rounded-full transition-all duration-500 ${barColor}`}
          style={{ width: `${pct}%` }}
        />
      </div>
      <div className="flex justify-end mt-1">
        <span className={`text-[10px] font-mono ${getPctColor(pct)}`}>
          {pct.toFixed(0)}%
        </span>
      </div>
    </div>
  );
}

export default function UsageDashboard() {
  const [usage, setUsage] = useState<UsageStatusData | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    api
      .getUsageStatus()
      .then(setUsage)
      .catch(() => setUsage(null))
      .finally(() => setLoading(false));
  }, []);

  if (loading) {
    return (
      <div className="flex items-center justify-center py-12">
        <span className="text-sm text-brand-shade3 font-mono">Loading usage data...</span>
      </div>
    );
  }

  if (!usage) {
    return (
      <div className="flex items-center justify-center py-12">
        <span className="text-sm text-brand-shade3 font-mono">Usage data unavailable</span>
      </div>
    );
  }

  // Plan management lives on the cloud dashboard; self-hosted deployments
  // configure limits via the engine and get no external link here.
  const landingUrl = import.meta.env.VITE_LANDING_URL as string | undefined;
  const managePlanUrl =
    import.meta.env.VITE_AUTH_MODE === 'external' && landingUrl ? `${landingUrl}/billing` : null;

  return (
    <div className="space-y-6">
      {managePlanUrl && (
        <div className="flex justify-end">
          <a
            href={managePlanUrl}
            target="_blank"
            rel="noreferrer"
            className="px-4 py-1.5 bg-brand-accent hover:bg-brand-accent-hover text-white rounded-btn text-xs font-medium font-mono transition-colors"
          >
            Manage Plan
          </a>
        </div>
      )}

      <div className="grid grid-cols-2 gap-4">
        {METRICS.map(({ key, label }) => (
          <UsageBar key={key} label={label} metric={usage[key]} />
        ))}
      </div>
    </div>
  );
}
