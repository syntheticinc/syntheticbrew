import { useState, useEffect, useRef } from 'react';
import { useBottomPanel } from '../hooks/useBottomPanel';
import { api } from '../api/client';

export default function SchemaSelector() {
  const { selectedSchema, setSelectedSchema } = useBottomPanel();
  const [schemas, setSchemas] = useState<{ id: string; name: string }[]>([]);
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    api.listSchemas()
      .then((list) => {
        const mapped = list.filter((s) => !s.is_system).map((s) => ({ id: String(s.id), name: s.name }));
        setSchemas(mapped);
        const first = mapped[0];
        if (!selectedSchema && first) {
          setSelectedSchema(first.name);
        }
      })
      .catch(() => {
        // API not available yet — empty list
      });
  }, [selectedSchema, setSelectedSchema]);

  // Close dropdown on outside click
  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [open]);

  const display = selectedSchema || 'Select schema...';

  return (
    <div ref={ref} className="relative">
      <button
        onClick={() => setOpen((o) => !o)}
        className="flex items-center gap-1.5 px-2.5 py-1 rounded-btn text-xs font-medium text-brand-shade2 hover:text-brand-light hover:bg-brand-dark-alt/60 transition-colors border border-brand-shade3/20"
      >
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
          <path d="M22 19a2 2 0 01-2 2H4a2 2 0 01-2-2V5a2 2 0 012-2h5l2 3h9a2 2 0 012 2z" />
        </svg>
        <span className="max-w-[140px] truncate">{display}</span>
        <svg width="10" height="10" viewBox="0 0 14 14" fill="none" className={`transition-transform ${open ? 'rotate-180' : ''}`}>
          <path d="M3 5L7 9L11 5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      </button>

      {open && schemas.length > 0 && (
        <div className="absolute top-full left-0 mt-1 min-w-[180px] bg-brand-dark-alt border border-brand-shade3/20 rounded-card shadow-xl z-50 py-1 animate-modal-in">
          {schemas.map((s) => (
            <button
              key={s.id}
              onClick={() => {
                setSelectedSchema(s.name);
                setOpen(false);
              }}
              className={[
                'w-full px-3 py-1.5 text-left text-xs transition-colors flex items-center gap-2',
                s.name === selectedSchema
                  ? 'text-white bg-brand-accent/10'
                  : 'text-brand-shade2 hover:bg-brand-dark-surface hover:text-brand-light',
              ].join(' ')}
            >
              {s.name === selectedSchema && (
                <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                  <polyline points="20 6 9 17 4 12" />
                </svg>
              )}
              {s.name !== selectedSchema && <span className="w-[10px]" />}
              {s.name}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
