import React, { createContext, useContext, useState, useCallback, useEffect, useRef } from 'react';

// ─── Types ─────────────────────────────────────────────────────────────────────

export type ToastType = 'success' | 'error' | 'warning' | 'info';

export interface Toast {
  id: string;
  message: string;
  type: ToastType;
}

interface ToastContextValue {
  addToast: (message: string, type?: ToastType) => void;
}

// ─── Context ───────────────────────────────────────────────────────────────────

const ToastContext = createContext<ToastContextValue | null>(null);

// ─── Hook ──────────────────────────────────────────────────────────────────────

export function useToast(): ToastContextValue {
  const ctx = useContext(ToastContext);
  if (!ctx) throw new Error('useToast must be used inside ToastProvider');
  return ctx;
}

// ─── Individual toast item ─────────────────────────────────────────────────────

const ICON: Record<ToastType, React.ReactNode> = {
  success: (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M20 6L9 17l-5-5" />
    </svg>
  ),
  error: (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="12" cy="12" r="10" />
      <line x1="15" y1="9" x2="9" y2="15" />
      <line x1="9" y1="9" x2="15" y2="15" />
    </svg>
  ),
  warning: (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M10.29 3.86L1.82 18a2 2 0 001.71 3h16.94a2 2 0 001.71-3L13.71 3.86a2 2 0 00-3.42 0z" />
      <line x1="12" y1="9" x2="12" y2="13" />
      <line x1="12" y1="17" x2="12.01" y2="17" />
    </svg>
  ),
  info: (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="12" cy="12" r="10" />
      <line x1="12" y1="16" x2="12" y2="12" />
      <line x1="12" y1="8" x2="12.01" y2="8" />
    </svg>
  ),
};

const COLOR: Record<ToastType, string> = {
  success: 'text-green-400 bg-green-500/10 border-green-500/25',
  error: 'text-red-400 bg-red-500/10 border-red-500/25',
  warning: 'text-amber-400 bg-amber-500/10 border-amber-500/25',
  info: 'text-blue-400 bg-blue-500/10 border-blue-500/25',
};

const ICON_COLOR: Record<ToastType, string> = {
  success: 'text-green-400',
  error: 'text-red-400',
  warning: 'text-amber-400',
  info: 'text-blue-400',
};

interface ToastItemProps {
  toast: Toast;
  onDismiss: (id: string) => void;
}

function ToastItem({ toast, onDismiss }: ToastItemProps) {
  const [visible, setVisible] = useState(false);
  const mountedRef = useRef(false);

  // Entry animation
  useEffect(() => {
    if (!mountedRef.current) {
      mountedRef.current = true;
      requestAnimationFrame(() => setVisible(true));
    }
  }, []);

  return (
    <div
      className={`flex items-start gap-2.5 px-3 py-2.5 rounded-card border text-xs min-w-[220px] max-w-[320px] shadow-xl transition-all duration-200 ${COLOR[toast.type]} ${
        visible ? 'opacity-100 translate-y-0' : 'opacity-0 translate-y-2'
      }`}
    >
      <span className={`mt-0.5 flex-shrink-0 ${ICON_COLOR[toast.type]}`}>{ICON[toast.type]}</span>
      <span className="flex-1 leading-relaxed">{toast.message}</span>
      <button
        onClick={() => onDismiss(toast.id)}
        className="flex-shrink-0 opacity-50 hover:opacity-100 transition-opacity mt-0.5"
        aria-label="Dismiss"
      >
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
          <line x1="18" y1="6" x2="6" y2="18" />
          <line x1="6" y1="6" x2="18" y2="18" />
        </svg>
      </button>
    </div>
  );
}

// ─── Container ─────────────────────────────────────────────────────────────────

interface ToastContainerProps {
  toasts: Toast[];
  onDismiss: (id: string) => void;
}

function ToastContainer({ toasts, onDismiss }: ToastContainerProps) {
  // The container uses the HTML popover API so it renders in the browser
  // top-layer. Without this, native <dialog>.showModal() (used by Modal.tsx)
  // promotes the dialog above any z-index — toasts shown while a modal is
  // open would be hidden behind the dialog backdrop. Popovers and modal
  // dialogs share the same top-layer; the most recently promoted element
  // wins, so a toast opened *after* a dialog floats correctly above it.
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    // Feature detection — fall back to plain z-index on older browsers.
    if (typeof el.showPopover !== 'function') return;
    if (toasts.length > 0) {
      try {
        el.showPopover();
      } catch {
        // Already shown — ignore. Browsers throw if calling showPopover()
        // on an already-open popover.
      }
    } else {
      try {
        el.hidePopover();
      } catch {
        // Not open — ignore.
      }
    }
  }, [toasts.length]);

  if (toasts.length === 0) return null;
  return (
    <div
      ref={ref}
      // `popover="manual"` puts this in the top-layer when showPopover() is
      // called and disables Escape-to-close (we have explicit dismiss
      // buttons + auto-dismiss). Inline style overrides the default
      // popover centering so the toasts stay anchored bottom-right.
      // eslint-disable-next-line react/no-unknown-property
      popover="manual"
      style={{
        position: 'fixed',
        inset: 'auto 1rem 1rem auto',
        margin: 0,
        padding: 0,
        background: 'transparent',
        border: 'none',
      }}
      className="z-[9999] flex flex-col gap-2 items-end pointer-events-none"
    >
      {toasts.map((t) => (
        <div key={t.id} className="pointer-events-auto">
          <ToastItem toast={t} onDismiss={onDismiss} />
        </div>
      ))}
    </div>
  );
}

// ─── Provider ──────────────────────────────────────────────────────────────────

const AUTO_DISMISS_MS = 4000;

export function ToastProvider({ children }: { children: React.ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const timers = useRef<Map<string, ReturnType<typeof setTimeout>>>(new Map());

  const dismiss = useCallback((id: string) => {
    setToasts((prev) => prev.filter((t) => t.id !== id));
    const timer = timers.current.get(id);
    if (timer !== undefined) {
      clearTimeout(timer);
      timers.current.delete(id);
    }
  }, []);

  const addToast = useCallback((message: string, type: ToastType = 'info') => {
    const id = `${Date.now()}-${Math.random().toString(36).slice(2)}`;
    setToasts((prev) => [...prev, { id, message, type }]);
    const timer = setTimeout(() => dismiss(id), AUTO_DISMISS_MS);
    timers.current.set(id, timer);
  }, [dismiss]);

  // Cleanup on unmount
  useEffect(() => {
    const currentTimers = timers.current;
    return () => {
      currentTimers.forEach((t) => clearTimeout(t));
    };
  }, []);

  return (
    <ToastContext.Provider value={{ addToast }}>
      {children}
      <ToastContainer toasts={toasts} onDismiss={dismiss} />
    </ToastContext.Provider>
  );
}
