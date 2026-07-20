import { useEffect, useState } from 'react';

const DISMISS_KEY = 'syntheticbrew_github_star_banner_dismissed';
const REPO_URL = 'https://github.com/syntheticinc/syntheticbrew';

const iconClass = 'w-[14px] h-[14px] shrink-0';

const StarIcon = (
  <svg
    className={`${iconClass} text-amber-300`}
    viewBox="0 0 24 24"
    fill="currentColor"
    stroke="none"
    aria-hidden="true"
  >
    <polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2" />
  </svg>
);

const CloseIcon = (
  <svg
    className={iconClass}
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth="1.8"
    strokeLinecap="round"
    strokeLinejoin="round"
    aria-hidden="true"
  >
    <line x1="18" y1="6" x2="6" y2="18" />
    <line x1="6" y1="6" x2="18" y2="18" />
  </svg>
);

// GitHubStarBanner shows a dismissible call-to-action asking the user to
// star the repository. Dismissal persists via localStorage so the banner
// appears at most once per browser profile. `inline` renders it as a bare
// row segment for embedding into the top bar (no own border/background).
export function GitHubStarBanner({ inline = false }: { inline?: boolean } = {}) {
  const [visible, setVisible] = useState(false);

  useEffect(() => {
    try {
      const dismissed = localStorage.getItem(DISMISS_KEY);
      if (!dismissed) setVisible(true);
    } catch {
      // localStorage may be unavailable (private mode, strict CSP). Fail
      // closed — don't show the banner rather than risk showing it every
      // reload.
    }
  }, []);

  function dismiss() {
    try {
      localStorage.setItem(DISMISS_KEY, 'true');
    } catch {
      // ignore — still hide for this session
    }
    setVisible(false);
  }

  if (!visible) return inline ? <span /> : null;

  return (
    <div
      role="region"
      aria-label="GitHub star call-to-action"
      className={
        inline
          ? 'flex items-center gap-3 min-w-0 text-[12px] text-brand-shade2'
          : 'flex items-center justify-between gap-3 border-b border-brand-shade3/15 bg-gradient-to-r from-brand-dark-alt via-brand-dark-surface to-brand-dark-alt px-4 py-2 text-[12px] text-brand-shade2 shrink-0'
      }
    >
      <div className="flex items-center gap-2 min-w-0">
        {StarIcon}
        <span className="truncate">
          Enjoying SyntheticBrew? Star us on GitHub and help others discover it —{' '}
          <a
            href={REPO_URL}
            target="_blank"
            rel="noopener noreferrer"
            className="font-medium text-brand-light underline underline-offset-2 decoration-brand-shade3/40 hover:decoration-brand-accent hover:text-brand-accent transition-colors"
          >
            github.com/syntheticinc/syntheticbrew
          </a>
        </span>
      </div>
      <button
        type="button"
        onClick={dismiss}
        className="text-brand-shade3 hover:text-brand-light transition-colors p-1 -m-1 rounded"
        aria-label="Dismiss GitHub star banner"
        title="Dismiss"
      >
        {CloseIcon}
      </button>
    </div>
  );
}

export default GitHubStarBanner;
