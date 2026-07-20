import { useState } from 'react';
import { Outlet } from 'react-router-dom';
import { currentTheme, setTheme, type ResolvedTheme } from '../lib/theme';
import OnboardCodingAgentButton from './OnboardCodingAgentButton';
import Sidebar from './Sidebar';
import BottomPanel from './BottomPanel';
import QuotaBanner from './QuotaBanner';
import { BottomPanelProvider } from '../hooks/useBottomPanel';

function ThemeToggle() {
  const [theme, setThemeState] = useState<ResolvedTheme>(currentTheme);

  function toggle() {
    const next: ResolvedTheme = theme === 'dark' ? 'light' : 'dark';
    setTheme(next);
    setThemeState(next);
  }

  return (
    <button
      onClick={toggle}
      aria-label={theme === 'dark' ? 'Use light theme' : 'Use dark theme'}
      title={theme === 'dark' ? 'Use light theme' : 'Use dark theme'}
      className="p-1.5 text-brand-shade3 hover:text-brand-light rounded-btn transition-colors"
      data-testid="theme-toggle"
    >
      {theme === 'dark' ? (
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <circle cx="12" cy="12" r="4" />
          <path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41" />
        </svg>
      ) : (
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" />
        </svg>
      )}
    </button>
  );
}

const StarIcon = (
  <svg
    className="w-[14px] h-[14px] shrink-0 text-amber-300"
    viewBox="0 0 24 24"
    fill="currentColor"
    stroke="none"
    aria-hidden="true"
  >
    <polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2" />
  </svg>
);

function TopHeader() {
  return (
    <div className="flex items-center justify-between gap-3 px-4 py-2 border-b border-brand-shade3/15 bg-gradient-to-r from-brand-dark-alt via-brand-dark-surface to-brand-dark-alt shrink-0">
      <div className="flex items-center gap-2 min-w-0 text-[12px] text-brand-shade2">
        {StarIcon}
        <span className="truncate">
          Enjoying SyntheticBrew? Star us on GitHub and help others discover it —{' '}
          <a
            href="https://github.com/syntheticinc/syntheticbrew"
            target="_blank"
            rel="noopener noreferrer"
            className="font-medium text-brand-light underline underline-offset-2 decoration-brand-shade3/40 hover:decoration-brand-accent hover:text-brand-accent transition-colors"
          >
            github.com/syntheticinc/syntheticbrew
          </a>
        </span>
      </div>
      <div className="flex items-center gap-3 shrink-0">
        <ThemeToggle />
        <OnboardCodingAgentButton compact />
      </div>
    </div>
  );
}

function LayoutInner() {
  return (
    <div className="flex h-screen overflow-hidden">
      <Sidebar />
      <div className="flex-1 flex flex-col min-w-0 min-h-0">
        <TopHeader />
        <QuotaBanner />
        <main className="flex-1 min-h-0 bg-brand-dark p-6 overflow-auto animate-fade-in">
          <Outlet />
        </main>
        <BottomPanel />
      </div>
    </div>
  );
}

export default function Layout() {
  return (
    <BottomPanelProvider>
      <LayoutInner />
    </BottomPanelProvider>
  );
}
