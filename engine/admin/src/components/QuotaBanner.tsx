import { useState, useEffect, useCallback } from 'react';
import { api } from '../api/client';
import type { UsageMetric } from '../types';

const DISMISS_KEY = 'syntheticbrew_quota_banner_dismissed';

export default function QuotaBanner() {
  const [metrics, setMetrics] = useState<UsageMetric[]>([]);
  const [dismissed, setDismissed] = useState(false);
  const [showModal, setShowModal] = useState(false);
  const [stripeUrl, setStripeUrl] = useState<string | undefined>();

  useEffect(() => {
    // Reappear on page load (clear previous dismissal)
    sessionStorage.removeItem(DISMISS_KEY);

    api
      .getUsage()
      .then((data) => {
        if (data && Array.isArray(data.metrics)) {
          setMetrics(data.metrics);
        }
        if (data?.stripe_portal_url) {
          setStripeUrl(data.stripe_portal_url);
        }
      })
      .catch(() => {});
  }, []);

  const dismiss = useCallback(() => {
    setDismissed(true);
    sessionStorage.setItem(DISMISS_KEY, 'true');
  }, []);

  const handleUpgrade = useCallback(() => {
    if (stripeUrl) {
      window.open(stripeUrl, '_blank');
    }
  }, [stripeUrl]);

  // Find the worst metric
  const worstMetric = metrics.reduce<{ metric: UsageMetric | null; pct: number }>(
    (acc, m) => {
      if (m.limit <= 0) return acc;
      const pct = (m.used / m.limit) * 100;
      if (pct > acc.pct) return { metric: m, pct };
      return acc;
    },
    { metric: null, pct: 0 },
  );

  // 100% → modal block
  if (worstMetric.pct >= 100 && worstMetric.metric) {
    if (!showModal) {
      // Auto-show modal on first detection
      setTimeout(() => setShowModal(true), 100);
    }

    return showModal ? (
      <div className="fixed inset-0 z-[100] flex items-center justify-center bg-black/60 backdrop-blur-sm">
        <div className="bg-brand-dark-surface border border-red-500/30 rounded-xl p-8 max-w-md w-full mx-4 shadow-2xl">
          <div className="flex items-center gap-3 mb-4">
            <div className="w-10 h-10 rounded-full bg-red-500/15 flex items-center justify-center">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="text-red-400">
                <circle cx="12" cy="12" r="10" />
                <line x1="12" y1="8" x2="12" y2="12" />
                <line x1="12" y1="16" x2="12.01" y2="16" />
              </svg>
            </div>
            <h2 className="text-lg font-semibold text-brand-light font-mono">Limit Reached</h2>
          </div>
          <p className="text-sm text-brand-shade2 mb-2 font-mono">
            You've used <span className="text-red-400 font-semibold">100%</span> of your{' '}
            <span className="text-brand-light">{worstMetric.metric.label}</span> limit.
          </p>
          <p className="text-xs text-brand-shade3 mb-6 font-mono">
            Upgrade your plan to continue using SyntheticBrew without interruption.
          </p>
          <div className="flex items-center gap-3">
            <button
              onClick={handleUpgrade}
              className="flex-1 px-4 py-2.5 bg-brand-accent hover:bg-brand-accent-hover text-brand-light rounded-btn text-sm font-semibold font-mono transition-colors"
            >
              Upgrade Plan
            </button>
            <button
              onClick={() => setShowModal(false)}
              className="px-4 py-2.5 text-brand-shade3 hover:text-brand-light text-sm font-mono transition-colors"
            >
              Dismiss
            </button>
          </div>
        </div>
      </div>
    ) : null;
  }

  if (dismissed || worstMetric.pct < 80 || !worstMetric.metric) {
    return null;
  }

  // 80-94% → yellow, 95-99% → red
  const isRed = worstMetric.pct >= 95;
  const bgClass = isRed ? 'bg-red-500/10 border-red-500/30' : 'bg-amber-500/10 border-amber-500/30';
  const textClass = isRed ? 'text-red-400' : 'text-amber-400';
  const message = isRed
    ? `Almost at limit — ${worstMetric.metric.label} at ${worstMetric.pct.toFixed(0)}%. Upgrade to avoid interruption.`
    : `You've used ${worstMetric.pct.toFixed(0)}% of your ${worstMetric.metric.label} limit this month.`;

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
        <button
          onClick={handleUpgrade}
          className={`px-3 py-1 rounded-btn text-xs font-medium font-mono transition-colors ${
            isRed
              ? 'bg-red-500/20 text-red-400 hover:bg-red-500/30'
              : 'bg-amber-500/20 text-amber-400 hover:bg-amber-500/30'
          }`}
        >
          Upgrade
        </button>
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
