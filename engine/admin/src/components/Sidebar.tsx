import { useEffect, useRef, useState } from 'react';
import { NavLink } from 'react-router-dom';
import { useAuth } from '../hooks/useAuth';
import { api } from '../api/client';

interface NavItem {
  to: string;
  label: string;
  icon: React.ReactNode;
}

interface NavSection {
  label: string;
  items: NavItem[];
}

const iconClass = "w-[18px] h-[18px]";

// Inline SVG icons (18x18, stroke-width 1.5, stroke=currentColor, fill=none)
const icons = {
  health: (
    <svg className={iconClass} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M3 12h4l3-9 4 18 3-9h4" />
    </svg>
  ),
  agents: (
    <svg className={iconClass} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <rect x="4" y="4" width="16" height="16" rx="2" />
      <rect x="9" y="9" width="6" height="6" rx="1" />
      <path d="M9 1v3M15 1v3M9 20v3M15 20v3M20 9h3M20 15h3M1 9h3M1 15h3" />
    </svg>
  ),
  mcp: (
    <svg className={iconClass} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M12 2v6M12 16v6" />
      <circle cx="12" cy="12" r="4" />
      <path d="M4.93 4.93l4.24 4.24M14.83 14.83l4.24 4.24M2 12h6M16 12h6M4.93 19.07l4.24-4.24M14.83 9.17l4.24-4.24" />
    </svg>
  ),
  models: (
    <svg className={iconClass} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M21 16V8a2 2 0 00-1-1.73l-7-4a2 2 0 00-2 0l-7 4A2 2 0 003 8v8a2 2 0 001 1.73l7 4a2 2 0 002 0l7-4A2 2 0 0021 16z" />
      <path d="M3.27 6.96L12 12.01l8.73-5.05M12 22.08V12" />
    </svg>
  ),
  tasks: (
    <svg className={iconClass} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M16 4h2a2 2 0 012 2v14a2 2 0 01-2 2H6a2 2 0 01-2-2V6a2 2 0 012-2h2" />
      <rect x="8" y="2" width="8" height="4" rx="1" />
      <path d="M9 14l2 2 4-4" />
    </svg>
  ),
  apiKeys: (
    <svg className={iconClass} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M21 2l-2 2m-7.61 7.61a5.5 5.5 0 11-7.778 7.778 5.5 5.5 0 017.777-7.777zm0 0L15.5 7.5m0 0l3 3L22 7l-3-3m-3.5 3.5L19 4" />
    </svg>
  ),
  settings: (
    <svg className={iconClass} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="12" cy="12" r="3" />
      <path d="M19.4 15a1.65 1.65 0 00.33 1.82l.06.06a2 2 0 010 2.83 2 2 0 01-2.83 0l-.06-.06a1.65 1.65 0 00-1.82-.33 1.65 1.65 0 00-1 1.51V21a2 2 0 01-4 0v-.09A1.65 1.65 0 009 19.4a1.65 1.65 0 00-1.82.33l-.06.06a2 2 0 01-2.83 0 2 2 0 010-2.83l.06-.06A1.65 1.65 0 004.68 15a1.65 1.65 0 00-1.51-1H3a2 2 0 010-4h.09A1.65 1.65 0 004.6 9a1.65 1.65 0 00-.33-1.82l-.06-.06a2 2 0 012.83-2.83l.06.06A1.65 1.65 0 009 4.68a1.65 1.65 0 001-1.51V3a2 2 0 014 0v.09a1.65 1.65 0 001 1.51 1.65 1.65 0 001.82-.33l.06-.06a2 2 0 012.83 2.83l-.06.06A1.65 1.65 0 0019.4 9a1.65 1.65 0 001.51 1H21a2 2 0 010 4h-.09a1.65 1.65 0 00-1.51 1z" />
    </svg>
  ),
  config: (
    <svg className={iconClass} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <line x1="4" y1="21" x2="4" y2="14" />
      <line x1="4" y1="10" x2="4" y2="3" />
      <line x1="12" y1="21" x2="12" y2="12" />
      <line x1="12" y1="8" x2="12" y2="3" />
      <line x1="20" y1="21" x2="20" y2="16" />
      <line x1="20" y1="12" x2="20" y2="3" />
      <line x1="1" y1="14" x2="7" y2="14" />
      <line x1="9" y1="8" x2="15" y2="8" />
      <line x1="17" y1="16" x2="23" y2="16" />
    </svg>
  ),
  audit: (
    <svg className={iconClass} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8z" />
      <polyline points="14 2 14 8 20 8" />
      <line x1="16" y1="13" x2="8" y2="13" />
      <line x1="16" y1="17" x2="8" y2="17" />
      <polyline points="10 9 9 9 8 9" />
    </svg>
  ),
  resilience: (
    <svg className={iconClass} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" />
    </svg>
  ),
  toolCallLog: (
    <svg className={iconClass} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="4 17 10 11 4 5" />
      <line x1="12" y1="19" x2="20" y2="19" />
    </svg>
  ),
  schemas: (
    <svg className={iconClass} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="5" cy="6" r="2" />
      <circle cx="19" cy="6" r="2" />
      <circle cx="12" cy="18" r="2" />
      <line x1="5" y1="8" x2="12" y2="16" />
      <line x1="19" y1="8" x2="12" y2="16" />
      <line x1="5" y1="6" x2="19" y2="6" />
    </svg>
  ),
  widget: (
    <svg className={iconClass} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M21 15a2 2 0 01-2 2H7l-4 4V5a2 2 0 012-2h14a2 2 0 012 2z" />
      <path d="M9 10h.01M15 10h.01M12 14h.01" />
    </svg>
  ),
  memory: (
    <svg className={iconClass} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <ellipse cx="12" cy="5" rx="9" ry="3" />
      <path d="M3 5v14a9 3 0 0018 0V5" />
      <path d="M3 12a9 3 0 0018 0" />
    </svg>
  ),
  knowledge: (
    <svg className={iconClass} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M2 3h6a4 4 0 014 4v14a3 3 0 00-3-3H2z" />
      <path d="M22 3h-6a4 4 0 00-4 4v14a3 3 0 013-3h7z" />
    </svg>
  ),
  knowledgeGraphs: (
    <svg className={iconClass} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="6" cy="6" r="2.5" />
      <circle cx="18" cy="6" r="2.5" />
      <circle cx="12" cy="18" r="2.5" />
      <line x1="7.8" y1="7.8" x2="10.5" y2="16" />
      <line x1="16.2" y1="7.8" x2="13.5" y2="16" />
    </svg>
  ),
  logout: (
    <svg className={iconClass} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M9 21H5a2 2 0 01-2-2V5a2 2 0 012-2h4" />
      <polyline points="16 17 21 12 16 7" />
      <line x1="21" y1="12" x2="9" y2="12" />
    </svg>
  ),
};

const overviewIcon = (
  <svg className={iconClass} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
    <rect x="3" y="3" width="7" height="9" rx="1" />
    <rect x="14" y="3" width="7" height="5" rx="1" />
    <rect x="14" y="12" width="7" height="9" rx="1" />
    <rect x="3" y="16" width="7" height="5" rx="1" />
  </svg>
);

const sections: NavSection[] = [
  {
    label: 'Core',
    items: [
      { to: '/overview', label: 'Overview', icon: overviewIcon },
      { to: '/schemas', label: 'Schemas', icon: icons.schemas },
    ],
  },
  {
    label: 'Resources',
    items: [
      { to: '/agents', label: 'Agents', icon: icons.agents },
      { to: '/mcp', label: 'MCP Servers', icon: icons.mcp },
      { to: '/models', label: 'Models', icon: icons.models },
      { to: '/knowledge', label: 'Knowledge', icon: icons.knowledge },
      { to: '/knowledge-graphs', label: 'Knowledge Graphs', icon: icons.knowledgeGraphs },
      { to: '/widget', label: 'Widgets', icon: icons.widget },
    ],
  },
  {
    label: 'Automation',
    items: [
      { to: '/tasks', label: 'Tasks', icon: icons.tasks },
    ],
  },
  {
    label: 'Security',
    items: [
      { to: '/api-keys', label: 'API Keys', icon: icons.apiKeys },
      { to: '/settings', label: 'Settings', icon: icons.settings },
      { to: '/config', label: 'Config', icon: icons.config },
      { to: '/audit', label: 'Audit Log', icon: icons.audit },
    ],
  },
  {
    label: 'Observability',
    items: [
      { to: '/resilience', label: 'Resilience', icon: icons.resilience },
      { to: '/tool-call-log', label: 'Tool Call Log', icon: icons.toolCallLog },
    ],
  },
];

export default function Sidebar() {
  const { logout } = useAuth();
  const [updateAvailable, setUpdateAvailable] = useState<string | null>(null);
  const intervalRef = useRef<ReturnType<typeof setInterval> | undefined>(undefined);

  useEffect(() => {
    const checkUpdate = () => {
      api.health()
        .then((h) => setUpdateAvailable(h.update_available ?? null))
        .catch(() => { /* ignore -- health page handles errors */ });
    };

    checkUpdate();
    intervalRef.current = setInterval(checkUpdate, 60000);
    return () => clearInterval(intervalRef.current);
  }, []);

  return (
    <aside className="w-56 bg-brand-dark flex flex-col min-h-screen border-r border-brand-shade3/10">
      {/* Logo */}
      <div className="px-5 py-6 border-b border-brand-shade3/10">
        {/* Two logo assets, CSS-switched by theme (the dark SVG is a composite
            that breaks under a CSS invert filter). */}
        <img src={import.meta.env.BASE_URL + 'logo-dark.svg'} alt="SyntheticBrew" className="h-8 logo-on-dark" />
        <img src={import.meta.env.BASE_URL + 'logo-light.png'} alt="SyntheticBrew" className="h-8 logo-on-light" />
        <span className="text-[10px] text-brand-shade3 mt-2 block tracking-[0.2em] uppercase font-medium">Admin Dashboard</span>
      </div>

      {/* Nav sections */}
      <nav className="flex-1 overflow-y-auto px-3 py-2">
        {sections.map((section) => (
          <div key={section.label} className="mb-4">
            <div className="px-2 mb-1.5 text-[10px] font-semibold text-brand-shade3/60 uppercase tracking-[0.15em]">
              {section.label}
            </div>
            <div className="space-y-0.5">
              {section.items.map((item) => (
                <NavLink
                  key={item.to}
                  to={item.to}
                  className={({ isActive }) =>
                    [
                      'flex items-center gap-2.5 px-2.5 py-[7px] rounded-btn text-[13px] font-medium transition-all duration-150',
                      isActive
                        ? 'bg-brand-dark-alt text-brand-light border-l-2 border-l-brand-accent pl-2'
                        : 'text-brand-shade2 hover:bg-brand-dark-surface hover:text-brand-light border-l-2 border-l-transparent',
                    ].join(' ')
                  }
                >
                  <span className="flex-shrink-0 text-brand-shade3">{item.icon}</span>
                  {item.label}
                </NavLink>
              ))}
            </div>
          </div>
        ))}
      </nav>

      {/* Update banner (conditional) */}
      {updateAvailable && (
        <div className="mx-3 mb-2 rounded-btn border border-amber-500/30 bg-amber-500/10 px-3 py-2.5">
          <p className="text-[11px] font-semibold text-amber-400 mb-1">v{updateAvailable} available</p>
          <code className="text-[10px] text-amber-400/70 block leading-tight">
            docker pull syntheticinc/syntheticbrew:{updateAvailable}
          </code>
        </div>
      )}

      {/* Logout */}
      <div className="px-3 pb-4 border-t border-brand-shade3/10 pt-3">
        <button
          onClick={logout}
          className="w-full flex items-center gap-2.5 px-2.5 py-[7px] text-[13px] text-brand-shade3 hover:text-brand-light hover:bg-brand-dark-surface rounded-btn transition-all duration-150 text-left"
        >
          <span className="flex-shrink-0">{icons.logout}</span>
          Logout
        </button>
      </div>
    </aside>
  );
}
