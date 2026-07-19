import { Link, Outlet } from 'react-router-dom';
import Sidebar from './Sidebar';
import BottomPanel from './BottomPanel';
import QuotaBanner from './QuotaBanner';
import GitHubStarBanner from './GitHubStarBanner';
import { PrototypeProvider, usePrototype } from '../hooks/usePrototype';
import { BottomPanelProvider } from '../hooks/useBottomPanel';

function TopHeader() {
  const { isPrototype, togglePrototype, prototypeEnabled } = usePrototype();

  return (
    <div className="flex items-center gap-3 px-4 py-1.5 border-b border-brand-shade3/10 bg-brand-dark-surface shrink-0 justify-end">
      <Link
        to="/api-keys"
        className="text-[11px] text-brand-shade2 hover:text-brand-light border border-brand-shade3/30 rounded-btn px-2.5 py-1 transition-colors"
        data-testid="topbar-connect-agent"
      >
        Connect coding agent
      </Link>
      {prototypeEnabled && (
        <>
          <span className="text-[11px] text-brand-shade3 font-mono">
            {isPrototype ? 'Prototype' : 'Production'}
          </span>
          <button
            onClick={togglePrototype}
            role="switch"
            aria-checked={isPrototype}
            aria-label="Toggle prototype mode"
            className={`relative inline-flex h-5 w-9 items-center rounded-full transition-colors ${
              isPrototype ? 'bg-purple-500' : 'bg-brand-shade3/40'
            }`}
            title={isPrototype ? 'Switch to Production mode' : 'Switch to Prototype mode'}
          >
            <span
              className={`inline-block h-3.5 w-3.5 rounded-full bg-white transition-transform ${
                isPrototype ? 'translate-x-4' : 'translate-x-0.5'
              }`}
            />
          </button>
          <span className="w-px h-4 bg-brand-shade3/15 mx-1" aria-hidden="true" />
        </>
      )}
    </div>
  );
}

function LayoutInner() {
  return (
    <div className="flex h-screen overflow-hidden">
      <Sidebar />
      <div className="flex-1 flex flex-col min-w-0 min-h-0">
        <TopHeader />
        <GitHubStarBanner />
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
    <PrototypeProvider>
      <BottomPanelProvider>
        <LayoutInner />
      </BottomPanelProvider>
    </PrototypeProvider>
  );
}
