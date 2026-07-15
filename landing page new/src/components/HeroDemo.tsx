import { useState, useEffect, useRef, useCallback, useMemo } from 'react';
import type { ReactNode } from 'react';
import '../styles/demos.css';

/* ------------------------------------------------------------------ */
/*  Types                                                              */
/* ------------------------------------------------------------------ */

type Tab = 'builder' | 'chat';

interface SchemaSummary {
  entry: { name: string; tools: string[]; memory?: string };
  delegates: Array<{ name: string; tools: string[] }>;
}

interface DemoStep {
  type:
    | 'input_typing'
    | 'input_send'
    | 'thinking'
    | 'tool_call'
    | 'tool_result'
    | 'text'
    | 'spawn'
    | 'sub_tool'
    | 'sub_result'
    | 'spawn_done'
    | 'response'
    | 'ask_buttons'
    | 'button_click'
    | 'builder_action'
    | 'schema_summary'
    | 'tab_switch'
    | 'term_out'
    | 'cc_banner';
  content?: string;
  tool?: string;
  options?: string[];
  tab?: Tab;
  delay: number;
  schema?: SchemaSummary;
  // 'terminal' marks shell input: echoed into the pane as a `$ command` line
  // instead of a prompt bubble once sent.
  variant?: 'terminal';
}

/* ------------------------------------------------------------------ */
/*  Scenario                                                           */
/* ------------------------------------------------------------------ */

const SCENARIO: DemoStep[] = [
  // ========== Tab 1: your coding agent — install the MCP server, then build ==========
  // Start from the developer's own project directory so it's clear the coding
  // agent is being driven from inside their working repo, then paste the
  // install command. SyntheticBrew Cloud uses OAuth — the agent opens a browser
  // to sign in, so there is no token to paste.
  { type: 'input_typing', variant: 'terminal', content: 'cd ~/projects/churn-dashboard', tab: 'builder', delay: 500 },
  { type: 'input_send', variant: 'terminal', tab: 'builder', delay: 400 },
  { type: 'input_typing', variant: 'terminal', content: 'claude mcp add --transport http syntheticbrew https://app.syntheticbrew.ai/api/v1/mcp/rpc', tab: 'builder', delay: 500 },
  { type: 'input_send', variant: 'terminal', tab: 'builder', delay: 500 },
  { type: 'term_out', content: 'Added HTTP MCP server "syntheticbrew" — sign-in required', tab: 'builder', delay: 1100 },
  { type: 'input_typing', variant: 'terminal', content: 'claude mcp login syntheticbrew', tab: 'builder', delay: 400 },
  { type: 'input_send', variant: 'terminal', tab: 'builder', delay: 500 },
  { type: 'term_out', content: 'Opening browser to sign in…  ✔ authenticated — connected, 34 tools', tab: 'builder', delay: 1300 },
  { type: 'input_typing', variant: 'terminal', content: 'claude', tab: 'builder', delay: 400 },
  { type: 'input_send', variant: 'terminal', tab: 'builder', delay: 500 },
  { type: 'cc_banner', tab: 'builder', delay: 1100 },

  { type: 'input_typing', content: 'Build an agent that answers churn questions using our sales data.', tab: 'builder', delay: 400 },
  { type: 'input_send', tab: 'builder', delay: 500 },
  { type: 'thinking', tab: 'builder', delay: 1400 },

  { type: 'text', content: "I'll provision an analytics agent and a research specialist on your SyntheticBrew workspace.", tab: 'builder', delay: 900 },
  { type: 'tool_call', tool: 'provision_agent', content: 'analytics', tab: 'builder', delay: 400 },
  { type: 'tool_result', tool: 'provision_agent', content: 'agent created, chat-ready', tab: 'builder', delay: 900 },

  { type: 'tool_call', tool: 'admin_create_agent', content: 'research-agent', tab: 'builder', delay: 400 },
  { type: 'tool_result', tool: 'admin_create_agent', content: 'agent created, tools attached', tab: 'builder', delay: 900 },

  { type: 'text', content: 'Wiring delegation and enabling memory so the agent remembers each question across sessions.', tab: 'builder', delay: 900 },
  { type: 'tool_call', tool: 'admin_create_agent_relation', content: 'analytics → research-agent', tab: 'builder', delay: 400 },
  { type: 'tool_result', tool: 'admin_create_agent_relation', content: 'delegation wired', tab: 'builder', delay: 700 },

  { type: 'tool_call', tool: 'admin_add_capability', content: 'analytics · memory', tab: 'builder', delay: 400 },
  { type: 'tool_result', tool: 'admin_add_capability', content: 'cross-session memory enabled', tab: 'builder', delay: 700 },

  {
    type: 'response',
    tab: 'builder',
    delay: 900,
    content: "Done — here's what I built. Switching to Chat to try it.",
  },

  // Visual summary of what was built — gives readers a concrete picture
  {
    type: 'schema_summary',
    tab: 'builder',
    delay: 4500,
    schema: {
      entry: {
        name: 'analytics',
        tools: ['get_churn_data', 'get_onboarding_nps'],
        memory: 'cross-session',
      },
      delegates: [
        {
          name: 'research-agent',
          tools: ['diff_pricing', 'check_tickets'],
        },
      ],
    },
  },

  // Switch tabs — chat starts fresh
  { type: 'tab_switch', content: 'chat', delay: 800 },

  // ========== Tab 2: Chat — using the agent that was just built ==========
  { type: 'input_typing', content: 'Why did enterprise churn spike in March?', tab: 'chat', delay: 500 },
  { type: 'input_send', tab: 'chat', delay: 700 },
  { type: 'thinking', tab: 'chat', delay: 2200 },

  { type: 'tool_call', tool: 'get_churn_data', content: 'enterprise, Q1', tab: 'chat', delay: 500 },
  { type: 'tool_result', tool: 'get_churn_data', content: '14 accounts churned, +340% vs Q4', tab: 'chat', delay: 1400 },
  { type: 'tool_call', tool: 'get_onboarding_nps', content: 'cohort: post-Feb', tab: 'chat', delay: 400 },
  { type: 'tool_result', tool: 'get_onboarding_nps', content: 'NPS 31 (was 72), tickets 3x avg', tab: 'chat', delay: 1200 },

  { type: 'thinking', tab: 'chat', delay: 2400 },
  { type: 'text', content: 'Hmm. All 14 accounts joined after the Feb pricing change. Let me check what changed...', tab: 'chat', delay: 1200 },

  { type: 'spawn', content: 'research-agent: diff pricing tiers Feb vs Jan', tab: 'chat', delay: 1800 },
  { type: 'thinking', tab: 'chat', delay: 1600 },
  { type: 'sub_tool', tool: 'diff_pricing', content: 'enterprise tier, Feb vs Jan', tab: 'chat', delay: 1200 },
  { type: 'sub_result', content: 'Removed: onboarding calls, CSM assignment', tab: 'chat', delay: 1800 },
  { type: 'thinking', tab: 'chat', delay: 1200 },
  { type: 'sub_tool', tool: 'check_tickets', content: 'post-Feb cohort', tab: 'chat', delay: 1000 },
  { type: 'sub_result', content: '8.3 tickets/acct vs 2.1 avg', tab: 'chat', delay: 1400 },
  { type: 'spawn_done', content: 'research-agent: 2 findings, high confidence', tab: 'chat', delay: 1000 },

  { type: 'thinking', tab: 'chat', delay: 1800 },
  {
    type: 'response',
    content:
      '**Root cause:** Feb pricing removed onboarding calls. Enterprise buyers felt abandoned.\n\n' +
      '**Impact:** ~$420K churn risk. **Fix:** $15K/q. ROI 28:1.',
    tab: 'chat',
    delay: 2000,
  },

  {
    type: 'ask_buttons',
    content: 'Next step?',
    options: ['Draft rescue plan', 'Alert CS team', 'Deep dive'],
    tab: 'chat',
    delay: 2000,
  },
  { type: 'button_click', content: 'Alert CS team', tab: 'chat', delay: 1000 },

  { type: 'tool_call', tool: 'notify_cs_team', content: '#cs-enterprise, priority: high', tab: 'chat', delay: 400 },
  { type: 'tool_result', tool: 'notify_cs_team', content: 'Sent to 4 CSMs', tab: 'chat', delay: 1200 },
  { type: 'text', content: 'CS team alerted. Rescue plan ready on request.', tab: 'chat', delay: 3500 },
];

const SPAWN_STEP_TYPES = new Set<DemoStep['type']>(['spawn', 'sub_tool', 'sub_result', 'spawn_done']);
const TYPEWRITER_TYPES = new Set<DemoStep['type']>(['input_typing', 'text', 'response']);
const USER_CHAR_MS = 55;
const AGENT_CHAR_MS = 12;
const IS_MOBILE = typeof window !== 'undefined' && window.innerWidth < 768;

/* ------------------------------------------------------------------ */
/*  Coffee spinner — rotating messages                                 */
/* ------------------------------------------------------------------ */

const BREW_PHRASES = [
  'Grinding beans...',
  'Brewing...',
  'Pulling a shot...',
  'Steaming...',
  'Almost ready...',
];

let brewCounter = 0;

function BrewingSpinner() {
  const phrase = BREW_PHRASES[brewCounter++ % BREW_PHRASES.length];
  return (
    <div className="demo-spin">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5">
        <path d="M17 8h1a4 4 0 010 8h-1" strokeLinecap="round" />
        <path d="M3 8h14v9a4 4 0 01-4 4H7a4 4 0 01-4-4V8z" />
        <path d="M7 2v3" strokeLinecap="round" className="demo-steam" />
        <path d="M10 1v3" strokeLinecap="round" className="demo-steam demo-steam--2" />
        <path d="M13 2v3" strokeLinecap="round" className="demo-steam demo-steam--3" />
      </svg>
      <span className="demo-spin-label">{phrase}</span>
    </div>
  );
}

/* ------------------------------------------------------------------ */
/*  Inline SVG icons                                                   */
/* ------------------------------------------------------------------ */

function CheckIcon() {
  return (
    <svg className="demo-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <path d="M20 6L9 17l-5-5" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

function BranchIcon() {
  return (
    <svg className="demo-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <circle cx="18" cy="18" r="3" />
      <circle cx="6" cy="6" r="3" />
      <circle cx="6" cy="18" r="3" />
      <path d="M6 9v3a3 3 0 0 0 3 3h6" />
    </svg>
  );
}

function WrenchIcon() {
  return (
    <svg className="demo-icon demo-icon--dim" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <path d="M14.7 6.3a1 1 0 000 1.4l1.6 1.6a1 1 0 001.4 0l3.77-3.77a6 6 0 01-7.94 7.94l-6.91 6.91a2.12 2.12 0 01-3-3l6.91-6.91a6 6 0 017.94-7.94l-3.76 3.76z" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

/* ------------------------------------------------------------------ */
/*  Tiny markdown renderer                                             */
/* ------------------------------------------------------------------ */

function renderMarkdown(raw: string): ReactNode[] {
  const lines = raw.split('\n');
  const nodes: ReactNode[] = [];

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (line === '') { nodes.push(<br key={`br-${i}`} />); continue; }

    const listMatch = line.match(/^(\d+)\.\s+(.+)$/);
    if (listMatch) {
      nodes.push(
        <div key={`li-${i}`} className="demo-md-li">
          <span className="demo-md-num">{listMatch[1]}.</span>
          {inlineBold(listMatch[2])}
        </div>,
      );
      continue;
    }
    nodes.push(<div key={`ln-${i}`}>{inlineBold(line)}</div>);
  }
  return nodes;
}

function inlineBold(text: string): ReactNode {
  const parts = text.split(/\*\*(.+?)\*\*/g);
  if (parts.length === 1) return text;
  return parts.map((p, i) => (i % 2 === 1 ? <strong key={i}>{p}</strong> : p));
}

/* ------------------------------------------------------------------ */
/*  Step renderers                                                     */
/* ------------------------------------------------------------------ */

/* ---- Claude Code-styled terminal pieces (builder tab) ---- */

function TermCmdLine({ text }: { text: string }) {
  return (
    <div className="demo-term-line">
      <span className="demo-term-prompt">$</span>
      <span className="demo-term-cmd">{text}</span>
    </div>
  );
}

function TermOutLine({ text }: { text: string }) {
  return <div className="demo-term-out">{text}</div>;
}

function CCBanner() {
  // Deliberately brand-neutral: evokes a coding-agent session banner without
  // reproducing any specific product's welcome screen or name.
  return (
    <div className="demo-cc-banner">
      <span className="demo-accent">✻</span> Coding agent session started &mdash; syntheticbrew MCP tools loaded
    </div>
  );
}

function CCPromptLine({ text }: { text: string }) {
  return (
    <div className="demo-cc-prompt">
      <span className="demo-term-prompt">&gt;</span>
      <span>{text}</span>
    </div>
  );
}

function CCToolBlock({ tool, args, result, done }: { tool: string; args?: string; result?: string; done?: boolean }) {
  // Terminal-style MCP tool-call rendering: ⏺ server · tool (MCP) then ⎿ result.
  return (
    <div className="demo-cc-tool">
      <div className="demo-cc-tool-head">
        <span className={`demo-cc-bullet${done ? ' is-done' : ''}`}>⏺</span>
        <span className="demo-cc-server">syntheticbrew ·</span>
        <span className="demo-cc-name">{tool}</span>
        <span className="demo-cc-mcp">(MCP)</span>
        {args && <span className="demo-cc-args">{args}</span>}
      </div>
      {done && result && (
        <div className="demo-cc-resultrow">
          <span className="demo-cc-elbow">⎿</span>
          <span className="demo-cc-result">{result}</span>
        </div>
      )}
    </div>
  );
}

function UserBubble({ text }: { text: string }) {
  return (
    <div className="demo-bubble-row">
      <div className="demo-bubble">{text}</div>
    </div>
  );
}

function ToolCallBlock({ tool, args, result, done }: { tool: string; args?: string; result?: string; done?: boolean }) {
  return (
    <div className="demo-tool">
      <div className="demo-tool-head">
        <span className="demo-tool-name">{tool}</span>
        {args && <span className="demo-tool-args">({args})</span>}
        {done && <span className="demo-tool-done">done</span>}
      </div>
      {done && result && (
        <div className="demo-tool-result">
          <span className="demo-tool-arrow">→ </span>{result}
        </div>
      )}
    </div>
  );
}

function BuilderActionBlock({ tool, args, done }: { tool: string; args?: string; done?: boolean }) {
  // Builder-specific: accent color for tool name to distinguish from agent tool calls
  return (
    <div className="demo-tool demo-tool--builder">
      <div className="demo-tool-head">
        <WrenchIcon />
        <span className="demo-tool-name">{tool}</span>
        {args && <span className="demo-tool-args">({args})</span>}
        {done && <span className="demo-tool-done">done</span>}
      </div>
    </div>
  );
}

function SpawnBlock({ content }: { content: string }) {
  return (
    <div className="demo-spawn">
      <BranchIcon />
      {content}
    </div>
  );
}

function SpawnDoneBlock({ content }: { content: string }) {
  return (
    <div className="demo-spawn demo-spawn--done">
      <CheckIcon />
      {content}
    </div>
  );
}

function AgentText({ text, isResponse }: { text: string; isResponse?: boolean }) {
  return <div className="demo-msg">{isResponse ? renderMarkdown(text) : text}</div>;
}

// Build 2-letter initials from agent name, admin-canvas style.
function agentInitials(name: string): string {
  const cleaned = name.replace(/[^a-zA-Z0-9\-_]/g, '');
  const parts = cleaned.split(/[-_]+/).filter(Boolean);
  if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase();
  return cleaned.slice(0, 2).toUpperCase();
}

function AgentNode({
  name,
  tools,
  memory,
  isEntry,
}: {
  name: string;
  tools: string[];
  memory?: string;
  isEntry?: boolean;
}) {
  return (
    <div className={`demo-node${isEntry ? ' is-entry' : ''}`}>
      <div className="demo-node-head">
        {/* Initials avatar — mirrors admin canvas */}
        <span className="demo-avatar">{agentInitials(name)}</span>
        <div className="demo-node-main">
          <div className="demo-node-titlerow">
            <span className="demo-node-name">{name}</span>
            {isEntry && <span className="demo-node-badge">entry</span>}
          </div>
          <div className="demo-node-model">glm-5</div>
        </div>
      </div>

      <div className="demo-node-hr"></div>

      {/* Tools + memory — compact rows */}
      <div className="demo-node-rows">
        <div className="demo-node-row">
          <span className="demo-node-key">tools</span>
          <span className="demo-node-tools">{tools.join(', ')}</span>
        </div>
        {memory && (
          <div className="demo-node-row">
            <span className="demo-node-key">memory</span>
            <span className="demo-node-mem">{memory}</span>
          </div>
        )}
      </div>
    </div>
  );
}

function DelegationConnector() {
  // Real vertical line with a centered arrow head — mirrors the admin canvas edge.
  return (
    <div className="demo-connector">
      <svg width="30" height="32" viewBox="0 0 30 32">
        <line x1="15" y1="0" x2="15" y2="24" stroke="rgba(155,146,133,0.55)" strokeWidth="1" />
        <polygon points="15,30 11,24 19,24" fill="rgba(155,146,133,0.75)" />
      </svg>
      <span className="demo-connector-label">delegates</span>
    </div>
  );
}

function SchemaSummaryCard({ schema }: { schema: SchemaSummary }) {
  return (
    <div className="demo-schema">
      <div className="demo-schema-title">Your schema</div>

      <div className="demo-schema-stack">
        <AgentNode
          name={schema.entry.name}
          tools={schema.entry.tools}
          memory={schema.entry.memory}
          isEntry
        />

        {schema.delegates.map((d) => (
          <div key={d.name} className="demo-schema-branch">
            <DelegationConnector />
            <AgentNode name={d.name} tools={d.tools} />
          </div>
        ))}
      </div>
    </div>
  );
}

function AskButtons({ content, options, selected }: { content: string; options: string[]; selected?: string }) {
  return (
    <div className="demo-ask">
      <div className="demo-ask-q">{content}</div>
      <div className="demo-ask-row">
        {options.map((opt) => {
          const isSelected = selected === opt;
          const isFaded = selected && !isSelected;
          return (
            <span
              key={opt}
              className={`demo-chip${isSelected ? ' is-selected' : ''}${isFaded ? ' is-faded' : ''}`}
            >
              {opt}
            </span>
          );
        })}
      </div>
    </div>
  );
}

/* ------------------------------------------------------------------ */
/*  Main Component                                                     */
/* ------------------------------------------------------------------ */

export default function HeroDemo() {
  const [activeTab, setActiveTab] = useState<Tab>('builder');
  const [userClickedTab, setUserClickedTab] = useState<Tab | null>(null);
  const [visibleSteps, setVisibleSteps] = useState(0);
  const [isPaused, setIsPaused] = useState(false);
  const [typingIndex, setTypingIndex] = useState(-1);
  const [typedChars, setTypedChars] = useState(0);
  const chatRef = useRef<HTMLDivElement>(null);
  const builderRef = useRef<HTMLDivElement>(null);
  const [selectedButton, setSelectedButton] = useState<string | undefined>();
  const [inputText, setInputText] = useState('');

  // On mobile (<768px), skip sub-agent spawn steps to keep the demo compact
  const scenario = useMemo(
    () => (IS_MOBILE ? SCENARIO.filter((s) => !SPAWN_STEP_TYPES.has(s.type)) : SCENARIO),
    [],
  );

  const handleMouseLeave = useCallback(() => {
    setIsPaused(false);
    setUserClickedTab(null);
  }, []);

  const resetDemo = useCallback(() => {
    setActiveTab('builder');
    setUserClickedTab(null);
    setVisibleSteps(0);
    setTypingIndex(-1);
    setTypedChars(0);
    setSelectedButton(undefined);
    setInputText('');
    brewCounter = 0;
  }, []);

  useEffect(() => {
    if (isPaused) return;

    if (visibleSteps >= scenario.length) {
      const t = setTimeout(resetDemo, 3000);
      return () => clearTimeout(t);
    }

    const step = scenario[visibleSteps];

    // Auto-switch tab as scenario progresses (unless user manually overrode)
    if (step.type === 'tab_switch') {
      const next = step.content as Tab;
      if (!userClickedTab) setActiveTab(next);
      const t = setTimeout(() => setVisibleSteps((v) => v + 1), step.delay);
      return () => clearTimeout(t);
    }

    // Keep demo tab in sync with current step's tab (first time we enter each tab)
    if (step.tab && activeTab !== step.tab && !userClickedTab) {
      setActiveTab(step.tab);
    }

    if (step.type === 'input_typing' && step.content) {
      if (typingIndex !== visibleSteps) {
        setTypingIndex(visibleSteps);
        setTypedChars(0);
        setInputText('');
        return;
      }
      if (typedChars < step.content.length) {
        const t = setTimeout(() => {
          setTypedChars((c) => c + 1);
          setInputText(step.content!.slice(0, typedChars + 1));
        }, USER_CHAR_MS);
        return () => clearTimeout(t);
      }
      const t = setTimeout(() => { setVisibleSteps((v) => v + 1); setTypingIndex(-1); }, step.delay);
      return () => clearTimeout(t);
    }

    if (step.type === 'input_send') {
      setInputText('');
      const t = setTimeout(() => setVisibleSteps((v) => v + 1), step.delay);
      return () => clearTimeout(t);
    }

    if (TYPEWRITER_TYPES.has(step.type) && step.content && step.type !== 'input_typing') {
      if (typingIndex !== visibleSteps) { setTypingIndex(visibleSteps); setTypedChars(0); return; }
      if (typedChars < step.content.length) {
        const t = setTimeout(() => setTypedChars((c) => c + 1), AGENT_CHAR_MS);
        return () => clearTimeout(t);
      }
      const t = setTimeout(() => { setVisibleSteps((v) => v + 1); setTypingIndex(-1); }, step.delay);
      return () => clearTimeout(t);
    }

    if (step.type === 'button_click') {
      setSelectedButton(step.content);
      const t = setTimeout(() => setVisibleSteps((v) => v + 1), step.delay);
      return () => clearTimeout(t);
    }

    const t = setTimeout(() => setVisibleSteps((v) => v + 1), step.delay);
    return () => clearTimeout(t);
  }, [visibleSteps, isPaused, typingIndex, typedChars, resetDemo, scenario, activeTab, userClickedTab]);

  useEffect(() => {
    const ref = activeTab === 'chat' ? chatRef : builderRef;
    ref.current?.scrollTo({ top: ref.current.scrollHeight, behavior: 'smooth' });
  }, [visibleSteps, typedChars, activeTab]);

  const getUserTextForSend = useCallback((sendIndex: number) => {
    for (let j = sendIndex - 1; j >= 0; j--) {
      if (scenario[j].type === 'input_typing') return scenario[j].content ?? '';
    }
    return '';
  }, [scenario]);

  const renderStepsForTab = useCallback((tab: Tab): ReactNode[] => {
    const elements: ReactNode[] = [];

    for (let i = 0; i < visibleSteps; i++) {
      const step = scenario[i];
      if (step.tab !== tab) continue;
      const key = `step-${tab}-${i}`;
      const text = step.content ?? '';

      // The builder tab renders as a Claude Code terminal session; the chat
      // tab keeps the product-widget chat styling.
      const isTerminal = tab === 'builder';

      switch (step.type) {
        case 'input_typing': break;
        case 'input_send':
          elements.push(
            step.variant === 'terminal'
              ? <TermCmdLine key={key} text={getUserTextForSend(i)} />
              : isTerminal
                ? <CCPromptLine key={key} text={getUserTextForSend(i)} />
                : <UserBubble key={key} text={getUserTextForSend(i)} />,
          );
          break;
        case 'thinking': break;
        case 'term_out': elements.push(<TermOutLine key={key} text={text} />); break;
        case 'cc_banner': elements.push(<CCBanner key={key} />); break;
        case 'tool_call': {
          const nextStep = i + 1 < visibleSteps ? scenario[i + 1] : null;
          if (nextStep?.type === 'tool_result') break;
          elements.push(
            isTerminal
              ? <CCToolBlock key={key} tool={step.tool ?? ''} args={text} />
              : <ToolCallBlock key={key} tool={step.tool ?? ''} args={text} />,
          );
          break;
        }
        case 'tool_result': {
          let callArgs = '';
          for (let j = i - 1; j >= 0; j--) {
            if (scenario[j].type === 'tool_call' || scenario[j].type === 'sub_tool') {
              callArgs = scenario[j].content ?? '';
              break;
            }
          }
          elements.push(
            isTerminal
              ? <CCToolBlock key={key} tool={step.tool ?? ''} args={callArgs} result={text} done />
              : <ToolCallBlock key={key} tool={step.tool ?? ''} args={callArgs} result={text} done />,
          );
          break;
        }
        case 'text': elements.push(<AgentText key={key} text={text} />); break;
        case 'spawn': elements.push(<SpawnBlock key={key} content={text} />); break;
        case 'sub_tool': {
          const nextStep = i + 1 < visibleSteps ? scenario[i + 1] : null;
          if (nextStep?.type === 'sub_result') break;
          elements.push(<div key={key} className="demo-indent"><ToolCallBlock tool={step.tool ?? ''} args={text} /></div>);
          break;
        }
        case 'sub_result': {
          let callArgs = '';
          let toolName = step.tool ?? '';
          for (let j = i - 1; j >= 0; j--) {
            if (scenario[j].type === 'sub_tool') {
              callArgs = scenario[j].content ?? '';
              toolName = scenario[j].tool ?? toolName;
              break;
            }
          }
          elements.push(<div key={key} className="demo-indent"><ToolCallBlock tool={toolName} args={callArgs} result={text} done /></div>);
          break;
        }
        case 'spawn_done': elements.push(<SpawnDoneBlock key={key} content={text} />); break;
        case 'response': elements.push(<AgentText key={key} text={text} isResponse />); break;
        case 'ask_buttons': elements.push(<AskButtons key={key} content={text} options={step.options ?? []} selected={selectedButton} />); break;
        case 'button_click': break;
        case 'builder_action': elements.push(<BuilderActionBlock key={key} tool={step.tool ?? ''} args={text} done />); break;
        case 'schema_summary':
          if (step.schema) elements.push(<SchemaSummaryCard key={key} schema={step.schema} />);
          break;
        case 'tab_switch': break;
      }
    }

    // Currently typing in this tab
    if (
      typingIndex >= 0 &&
      typingIndex === visibleSteps &&
      typingIndex < scenario.length &&
      scenario[typingIndex].tab === tab
    ) {
      const step = scenario[typingIndex];
      if (step.type !== 'input_typing') {
        const partial = (step.content ?? '').slice(0, typedChars);
        elements.push(<AgentText key={`typing-${tab}-${typingIndex}`} text={partial + '▌'} isResponse={step.type === 'response'} />);
      }
    }

    // Active thinking spinner in this tab
    if (
      visibleSteps < scenario.length &&
      scenario[visibleSteps].type === 'thinking' &&
      scenario[visibleSteps].tab === tab &&
      typingIndex < 0
    ) {
      elements.push(<BrewingSpinner key={`brew-${tab}-${visibleSteps}`} />);
    }

    return elements;
  }, [visibleSteps, typingIndex, typedChars, selectedButton, getUserTextForSend, scenario]);

  const builderSteps = useMemo(() => renderStepsForTab('builder'), [renderStepsForTab]);
  const chatSteps = useMemo(() => renderStepsForTab('chat'), [renderStepsForTab]);

  const inputDisplay = useMemo(() => {
    if (
      typingIndex >= 0 &&
      typingIndex < scenario.length &&
      scenario[typingIndex].type === 'input_typing' &&
      scenario[typingIndex].tab === activeTab
    ) {
      return inputText + '▌';
    }
    return '';
  }, [typingIndex, inputText, scenario, activeTab]);

  const isInputActive = inputDisplay.length > 0;

  const inputPlaceholder = activeTab === 'builder'
    ? 'Tell your coding agent what to ship...'
    : 'Type a message...';

  const headerLabel = activeTab === 'builder'
    ? <>Your coding agent <span className="demo-dim">&middot; syntheticbrew &middot; MCP</span></>
    : <>Churn Analyst <span className="demo-dim">&middot; analytics &middot; glm-5</span></>;

  return (
    <div
      className="demo-window"
      onMouseEnter={() => setIsPaused(true)}
      onMouseLeave={handleMouseLeave}
    >
      {/* macOS-style title bar — fixed height, Paused indicator is absolute to avoid layout shift */}
      <div className="demo-titlebar">
        <div className="demo-dot demo-dot--red"></div>
        <div className="demo-dot demo-dot--yellow"></div>
        <div className="demo-dot demo-dot--green"></div>
        <span className="demo-paused" style={{ opacity: isPaused ? 1 : 0 }}>
          Paused &mdash; move cursor away to resume
        </span>
      </div>

      {/* Header with tabs */}
      <div className="demo-tabsbar">
        <span className="demo-window-title">{headerLabel}</span>
        <div className="demo-tabs">
          {(['builder', 'chat'] as const).map((tab) => {
            const isActive = activeTab === tab;
            const label = tab === 'builder' ? 'Coding agent' : 'Chat';
            return (
              <button
                key={tab}
                className={`demo-tab${isActive ? ' is-active' : ''}`}
                onClick={() => {
                  setActiveTab(tab);
                  setUserClickedTab(tab);
                }}
                tabIndex={-1}
              >
                {label}
              </button>
            );
          })}
        </div>
      </div>

      {/* Content — Builder or Chat */}
      {activeTab === 'builder' ? (
        <>
          <div ref={builderRef} className="demo-body">
            {builderSteps}
          </div>
          <div className="demo-inputbar">
            <div className={`demo-input${isInputActive ? ' is-typing is-accent' : ''}`}>
              {isInputActive ? inputDisplay : inputPlaceholder}
            </div>
            <button className="demo-send" tabIndex={-1}>Send</button>
          </div>
        </>
      ) : (
        <>
          <div ref={chatRef} className="demo-body">
            {chatSteps}
          </div>
          <div className="demo-inputbar">
            <div className={`demo-input${isInputActive ? ' is-typing is-accent' : ''}`}>
              {isInputActive ? inputDisplay : inputPlaceholder}
            </div>
            <button className="demo-send" tabIndex={-1}>Send</button>
          </div>
        </>
      )}
    </div>
  );
}
