/**
 * Theme selection shared across sibling subdomains of the same deployment.
 *
 * Source of truth is the `sbrew_theme` cookie: it is set with the parent
 * domain where possible, so an engine served on a subdomain (e.g.
 * app.example.com) sees the theme chosen on the parent site (example.com)
 * and vice versa. localStorage keeps a same-origin fallback for contexts
 * where cookies are unavailable. Default is dark (the admin's native look).
 */

export type ResolvedTheme = 'light' | 'dark';

const COOKIE_NAME = 'sbrew_theme';
const STORAGE_KEY = 'syntheticbrew-theme';
const COOKIE_MAX_AGE = 60 * 60 * 24 * 365;

function readCookie(): string | null {
  const match = document.cookie.match(new RegExp(`(?:^|;\\s*)${COOKIE_NAME}=(light|dark)`));
  return match ? match[1]! : null;
}

function readStorage(): string | null {
  try {
    return localStorage.getItem(STORAGE_KEY);
  } catch {
    return null;
  }
}

/** Cookie domain that makes the theme visible to sibling subdomains. */
function sharedCookieDomain(): string | null {
  const host = window.location.hostname;
  const labels = host.split('.');
  if (labels.length >= 3) return labels.slice(1).join('.');
  if (labels.length === 2) return host;
  return null; // localhost / single-label host: host-only cookie (port-agnostic)
}

function writeCookie(theme: ResolvedTheme): void {
  const base = `${COOKIE_NAME}=${theme}; path=/; max-age=${COOKIE_MAX_AGE}; samesite=lax`;
  const domain = sharedCookieDomain();
  if (domain) {
    document.cookie = `${base}; domain=${domain}`;
  }
  // If the domain write was rejected (e.g. public-suffix domain), fall back
  // to a host-only cookie so the choice at least persists locally.
  if (readCookie() !== theme) {
    document.cookie = base;
  }
}

function apply(theme: ResolvedTheme): void {
  document.documentElement.classList.toggle('light', theme === 'light');
}

/** Resolve the current theme: shared cookie > local storage > dark. */
export function currentTheme(): ResolvedTheme {
  const cookie = readCookie();
  if (cookie === 'light' || cookie === 'dark') return cookie;
  const stored = readStorage();
  if (stored === 'light' || stored === 'dark') return stored;
  return 'dark';
}

/** Apply the persisted theme to the document. Call once on startup. */
export function initTheme(): ResolvedTheme {
  const theme = currentTheme();
  apply(theme);
  return theme;
}

/** Persist and apply a theme choice. */
export function setTheme(theme: ResolvedTheme): void {
  writeCookie(theme);
  try {
    localStorage.setItem(STORAGE_KEY, theme);
  } catch {
    // localStorage unavailable — cookie already carries the choice
  }
  apply(theme);
}
