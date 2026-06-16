import { memo } from 'react';

function formatTokens(n: number): string {
  if (n >= 1000) return `${(n / 1000).toFixed(n % 1000 === 0 ? 0 : 1)}K`;
  return String(n);
}

function usageColor(pct: number): string {
  if (pct >= 85) return 'bg-red-500';
  if (pct >= 60) return 'bg-yellow-500';
  return 'bg-emerald-500';
}

interface ContextUsageBarProps {
  maxContextTokens: number | null;
  totalTokens?: number | null;
  contextTokens?: number | null;
  cachedTokens?: number | null;
  baselineTokens?: number | null;
}

export default memo(function ContextUsageBar({ maxContextTokens, totalTokens, contextTokens, cachedTokens, baselineTokens }: ContextUsageBarProps) {
  if (!maxContextTokens) return null;

  // Priority: contextTokens (real) > totalTokens (cumulative fallback) > baselineTokens (system prompt estimate)
  const displayTokens = contextTokens ?? totalTokens ?? baselineTokens;
  const pct = displayTokens ? Math.min(100, (displayTokens / maxContextTokens) * 100) : 0;

  // Cached is a subset of the current context — never show it larger than the
  // displayed context (defense-in-depth against an upstream over-count).
  const shownCached = cachedTokens != null && displayTokens != null
    ? Math.min(cachedTokens, displayTokens)
    : cachedTokens;

  return (
    <div className="px-3 py-1 flex items-center gap-2 border-t border-brand-shade3/10 flex-shrink-0">
      <div className="flex-1 h-1 bg-brand-shade3/10 rounded-full overflow-hidden">
        {pct > 0 && (
          <div
            className={`h-full rounded-full ${usageColor(pct)}`}
            style={{ width: `${pct}%` }}
          />
        )}
      </div>
      <span className="text-[10px] text-brand-shade3 whitespace-nowrap">
        {displayTokens ? formatTokens(displayTokens) : '\u2014'} / {formatTokens(maxContextTokens)} context
      </span>
      {shownCached != null && shownCached > 0 && (
        <span className="text-[10px] text-brand-shade3 whitespace-nowrap">
          {formatTokens(shownCached)} cached
        </span>
      )}
    </div>
  );
});
