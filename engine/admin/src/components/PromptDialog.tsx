import { type FormEvent, useEffect, useState } from 'react';
import Modal from './Modal';

interface PromptDialogProps {
  open: boolean;
  onClose: () => void;
  onSubmit: (value: string) => void;
  title: string;
  label: string;
  placeholder?: string;
  required?: boolean;
  submitLabel?: string;
  variant?: 'danger' | 'warning' | 'default';
  multiline?: boolean;
  initial?: string;
  loading?: boolean;
}

/**
 * PromptDialog — tailwind-styled replacement for window.prompt.
 * - Controlled value, reset on open
 * - Required validation with inline error
 * - Supports single-line and multiline input
 * - Variant controls submit button color (danger/warning/default)
 */
export default function PromptDialog({
  open,
  onClose,
  onSubmit,
  title,
  label,
  placeholder,
  required = false,
  submitLabel = 'OK',
  variant = 'default',
  multiline = false,
  initial = '',
  loading,
}: PromptDialogProps) {
  const [value, setValue] = useState(initial);
  const [touched, setTouched] = useState(false);

  useEffect(() => {
    if (open) {
      setValue(initial);
      setTouched(false);
    }
  }, [open, initial]);

  const missing = required && value.trim() === '';
  const showError = touched && missing;

  let btnClass: string;
  switch (variant) {
    case 'danger':
      btnClass = 'bg-red-600 hover:bg-red-700 text-white';
      break;
    case 'warning':
      btnClass = 'bg-amber-600 hover:bg-amber-700 text-white';
      break;
    default:
      btnClass = 'bg-brand-accent hover:bg-brand-accent-hover text-white';
  }

  function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setTouched(true);
    if (missing) return;
    onSubmit(value);
  }

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={title}
      className="max-w-md"
      footer={
        <div className="flex justify-end gap-3">
          <button
            type="button"
            onClick={onClose}
            className="px-4 py-2 text-sm text-brand-shade2 border border-brand-shade3/30 rounded-btn hover:bg-brand-dark hover:text-brand-light transition-colors"
          >
            Cancel
          </button>
          <button
            type="submit"
            form="prompt-dialog-form"
            disabled={loading || missing}
            className={`px-4 py-2 text-sm rounded-btn font-medium disabled:opacity-50 transition-colors ${btnClass}`}
          >
            {loading ? 'Processing...' : submitLabel}
          </button>
        </div>
      }
    >
      <form id="prompt-dialog-form" onSubmit={handleSubmit}>
        <label className="block text-xs font-medium text-brand-shade2 mb-2">
          {label}
          {required && <span className="text-red-400 ml-1">*</span>}
        </label>
        {multiline ? (
          <textarea
            autoFocus
            rows={4}
            value={value}
            onChange={(e) => setValue(e.target.value)}
            onBlur={() => setTouched(true)}
            placeholder={placeholder}
            className={`w-full px-3 py-2 bg-brand-dark border rounded-btn text-sm text-brand-light focus:outline-none transition-colors ${
              showError ? 'border-red-500' : 'border-brand-shade3/30 focus:border-brand-accent'
            }`}
          />
        ) : (
          <input
            autoFocus
            type="text"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            onBlur={() => setTouched(true)}
            placeholder={placeholder}
            className={`w-full px-3 py-2 bg-brand-dark border rounded-btn text-sm text-brand-light focus:outline-none transition-colors ${
              showError ? 'border-red-500' : 'border-brand-shade3/30 focus:border-brand-accent'
            }`}
          />
        )}
        {showError && (
          <p className="mt-1 text-xs text-red-400">This field is required.</p>
        )}
      </form>
    </Modal>
  );
}
