import { createContext, useContext, useState, useCallback, useEffect, type ReactNode } from 'react';

export type PanelTab = 'assistant' | 'testflow';

interface BottomPanelState {
  height: number;
  tab: PanelTab;
  collapsed: boolean;
}

interface BottomPanelContextValue {
  height: number;
  tab: PanelTab;
  collapsed: boolean;
  selectedSchema: string;
  setHeight: (h: number) => void;
  setTab: (t: PanelTab) => void;
  setCollapsed: (c: boolean) => void;
  toggleCollapsed: () => void;
  setSelectedSchema: (s: string) => void;
}

const STORAGE_KEY = 'syntheticbrew_panel_state';
const SCHEMA_KEY = 'syntheticbrew_panel_schema';

// Default panel state: expanded and focused on AI Assistant tab.
// The panel is the primary in-admin UX surface — users need to see where to
// chat/test immediately on first admin load. Users who prefer a larger canvas
// can collapse manually; the state persists via localStorage.
const DEFAULT_STATE: BottomPanelState = {
  height: 320,
  tab: 'assistant',
  collapsed: false,
};

function loadState(): BottomPanelState {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return DEFAULT_STATE;
    const parsed = JSON.parse(raw) as Partial<BottomPanelState>;
    return {
      height: typeof parsed.height === 'number' ? parsed.height : DEFAULT_STATE.height,
      tab: parsed.tab === 'assistant' || parsed.tab === 'testflow' ? parsed.tab : DEFAULT_STATE.tab,
      collapsed: typeof parsed.collapsed === 'boolean' ? parsed.collapsed : DEFAULT_STATE.collapsed,
    };
  } catch {
    return DEFAULT_STATE;
  }
}

function saveState(state: BottomPanelState) {
  localStorage.setItem(STORAGE_KEY, JSON.stringify(state));
}

const BottomPanelContext = createContext<BottomPanelContextValue>({
  ...DEFAULT_STATE,
  selectedSchema: '',
  setHeight: () => {},
  setTab: () => {},
  setCollapsed: () => {},
  toggleCollapsed: () => {},
  setSelectedSchema: () => {},
});

export function BottomPanelProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<BottomPanelState>(loadState);
  const [selectedSchema, setSelectedSchemaRaw] = useState(() =>
    localStorage.getItem(SCHEMA_KEY) ?? '',
  );

  useEffect(() => {
    saveState(state);
  }, [state]);

  const setHeight = useCallback((h: number) => {
    setState((prev) => ({ ...prev, height: h }));
  }, []);

  const setTab = useCallback((t: PanelTab) => {
    setState((prev) => ({ ...prev, tab: t }));
  }, []);

  const setCollapsed = useCallback((c: boolean) => {
    setState((prev) => ({ ...prev, collapsed: c }));
  }, []);

  const toggleCollapsed = useCallback(() => {
    setState((prev) => ({ ...prev, collapsed: !prev.collapsed }));
  }, []);

  const setSelectedSchema = useCallback((s: string) => {
    setSelectedSchemaRaw(s);
    localStorage.setItem(SCHEMA_KEY, s);
  }, []);

  return (
    <BottomPanelContext.Provider
      value={{
        height: state.height,
        tab: state.tab,
        collapsed: state.collapsed,
        selectedSchema,
        setHeight,
        setTab,
        setCollapsed,
        toggleCollapsed,
        setSelectedSchema,
      }}
    >
      {children}
    </BottomPanelContext.Provider>
  );
}

export function useBottomPanel() {
  return useContext(BottomPanelContext);
}
