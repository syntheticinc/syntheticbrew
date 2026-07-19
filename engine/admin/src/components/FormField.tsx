interface FormFieldProps {
  label: string;
  type?: 'text' | 'number' | 'password' | 'textarea' | 'select';
  value: string | number;
  onChange: (value: string) => void;
  placeholder?: string;
  disabled?: boolean;
  required?: boolean;
  options?: { value: string; label: string }[];
  hint?: string;
  error?: string;
  rows?: number;
  min?: number;
  max?: number;
  step?: number;
  pattern?: string;
  className?: string;
}

const inputClasses =
  'w-full px-3 py-2 bg-brand-dark-alt border border-brand-shade3/50 rounded-btn text-sm text-brand-light placeholder-brand-shade3 focus:outline-none focus:border-brand-accent focus:ring-1 focus:ring-brand-accent disabled:bg-brand-dark disabled:text-brand-shade3 disabled:opacity-60 transition-colors';

export default function FormField({
  label,
  type = 'text',
  value,
  onChange,
  placeholder,
  disabled,
  required,
  options,
  hint,
  error,
  rows = 3,
  min,
  max,
  step,
  pattern,
  className = '',
}: FormFieldProps) {
  const id = `ff-${label.toLowerCase().replace(/\s+/g, '-')}`;
  const hasError = !!error;

  const errorBorder = hasError ? 'border-red-400 focus:border-red-500 focus:ring-red-500' : '';

  return (
    <div className={className}>
      <label htmlFor={id} className="block text-sm font-medium text-brand-light mb-1">
        {label}
        {required && <span className="text-brand-accent ml-0.5">*</span>}
      </label>

      {type === 'select' ? (
        <select
          id={id}
          value={String(value)}
          onChange={(e) => onChange(e.target.value)}
          disabled={disabled}
          required={required}
          className={`${inputClasses} ${errorBorder}`}
        >
          {(options ?? []).map((opt) => (
            <option key={opt.value} value={opt.value}>
              {opt.label}
            </option>
          ))}
        </select>
      ) : type === 'textarea' ? (
        <textarea
          id={id}
          value={String(value)}
          onChange={(e) => onChange(e.target.value)}
          placeholder={placeholder}
          disabled={disabled}
          required={required}
          rows={rows}
          className={`${inputClasses} ${errorBorder}`}
        />
      ) : (
        <input
          id={id}
          type={type}
          value={type === 'number' ? value : String(value)}
          onChange={(e) => onChange(e.target.value)}
          placeholder={placeholder}
          disabled={disabled}
          required={required}
          min={min}
          max={max}
          step={step}
          pattern={pattern}
          className={`${inputClasses} ${errorBorder}`}
        />
      )}

      {hint && !hasError && (
        <p className="mt-1 text-xs text-brand-shade3">{hint}</p>
      )}
      {hasError && (
        <p className="mt-1 text-xs text-red-400">{error}</p>
      )}
    </div>
  );
}
