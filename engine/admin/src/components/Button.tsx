import type { ButtonHTMLAttributes } from 'react';

type Variant = 'primary' | 'secondary' | 'danger' | 'ghost';

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
}

/* Accent and danger surfaces are theme-invariant, so their text is always
   white — never a theme text token, which inverts in the light theme. */
const VARIANTS: Record<Variant, string> = {
  primary:
    'bg-brand-accent text-white hover:bg-brand-accent-hover disabled:opacity-50',
  secondary:
    'border border-brand-shade3/30 text-brand-shade2 hover:bg-brand-dark-alt hover:text-brand-light disabled:opacity-50',
  danger: 'bg-red-600 text-white hover:bg-red-700 disabled:opacity-50',
  ghost: 'text-brand-shade2 hover:text-brand-light disabled:opacity-50',
};

export default function Button({
  variant = 'primary',
  className = '',
  type = 'button',
  ...rest
}: ButtonProps) {
  return (
    <button
      type={type}
      className={`px-4 py-2 rounded-btn text-sm font-medium transition-colors cursor-pointer disabled:cursor-not-allowed ${VARIANTS[variant]} ${className}`}
      {...rest}
    />
  );
}
