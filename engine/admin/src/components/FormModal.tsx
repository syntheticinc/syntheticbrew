import { type FormEvent } from 'react';
import Modal from './Modal';

interface FormModalProps {
  open: boolean;
  onClose: () => void;
  title: string;
  onSubmit: (e: FormEvent) => void;
  submitLabel?: string;
  loading?: boolean;
  children: React.ReactNode;
  size?: 'sm' | 'md' | 'lg';
}

export default function FormModal({
  open,
  onClose,
  title,
  onSubmit,
  submitLabel = 'Save',
  loading,
  children,
  size = 'md',
}: FormModalProps) {
  const sizeClass = size === 'sm' ? 'max-w-sm' : size === 'lg' ? 'max-w-2xl' : 'max-w-lg';

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={title}
      className={sizeClass}
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
            form="form-modal-form"
            disabled={loading}
            className="px-4 py-2 text-sm text-white bg-brand-accent rounded-btn hover:bg-brand-accent-hover disabled:opacity-50 transition-colors font-medium"
          >
            {loading ? 'Saving...' : submitLabel}
          </button>
        </div>
      }
    >
      <form
        id="form-modal-form"
        onSubmit={onSubmit}
        className="space-y-4"
      >
        {children}
      </form>
    </Modal>
  );
}
