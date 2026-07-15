import { useState, useEffect, useRef, useCallback, useMemo } from 'react';
import type { ReactNode, RefObject } from 'react';
import '../styles/demos.css';

/* ------------------------------------------------------------------ */
/*  Animated hero demo — synchronized "without vs with grounding".     */
/*  One island renders BOTH panels off a shared cycle: they start       */
/*  together, each plays at its own pace, the finished one holds, and   */
/*  both restart together once both are done. The grounded panel acts   */
/*  as a consultant: finds a fit, hits a delivery problem, delegates to  */
/*  a logistics sub-agent, finds a faster warehouse, and closes the      */
/*  sale. Same visual language as the main landing's HeroDemo.          */
/* ------------------------------------------------------------------ */

type Variant = 'plain' | 'grounded';

interface Product {
  name: string;
  price: string;
  tags: string[];
  img?: string;
  pick?: boolean;
}

interface DemoStep {
  type:
    | 'input_typing'
    | 'input_send'
    | 'thinking'
    | 'tool_call'
    | 'tool_result'
    | 'text'
    | 'note'
    | 'response'
    | 'ask_buttons'
    | 'button_click'
    | 'product_cards'
    | 'spawn'
    | 'spawn_done';
  content?: string;
  tool?: string;
  options?: string[];
  delay: number;
  products?: Product[];
}

const QUESTION = 'Waterproof hiking jacket under $150, by Friday?';

const PLAIN_SCENARIO: DemoStep[] = [
  { type: 'input_typing', content: QUESTION, delay: 400 },
  { type: 'input_send', delay: 700 },
  { type: 'thinking', delay: 2200 },
  {
    type: 'response',
    content: 'Sure! Try the **Apex Stormbreaker ($119)** — lightweight and waterproof. It should arrive in a few days.',
    delay: 1600,
  },
  { type: 'note', content: '⚠ Invented — and it can’t check your catalog, stock, warehouses or delivery. So it can’t promise Friday, and it can’t place the order.', delay: 2500 },
];

const GROUNDED_SCENARIO: DemoStep[] = [
  { type: 'input_typing', content: QUESTION, delay: 400 },
  { type: 'input_send', delay: 700 },
  { type: 'thinking', delay: 1700 },
  { type: 'tool_call', tool: 'search_products', content: 'jackets, waterproof, <$150', delay: 500 },
  { type: 'tool_result', tool: 'search_products', content: '3 matches, all in stock', delay: 1200 },
  {
    type: 'product_cards',
    delay: 2200,
    products: [
      { name: 'Trailshell Lite', price: '$139', tags: ['290g', '15k wp', 'packable'], img: '/build-img/jacket-1-white.png', pick: true },
      { name: 'Stormpeak Shell', price: '$129', tags: ['410g', '20k wp', 'insulated'], img: '/build-img/jacket-2-white.png' },
      { name: 'Ridgeline Rain', price: '$99', tags: ['520g', '10k wp'], img: '/build-img/jacket-3-white.png' },
    ],
  },
  { type: 'thinking', delay: 1300 },
  { type: 'text', content: 'Best for hiking: Trailshell Lite — lightest and packable. Checking it makes your Friday deadline.', delay: 700 },
  { type: 'tool_call', tool: 'check_delivery', content: 'Trailshell Lite, EU-Central', delay: 500 },
  { type: 'tool_result', tool: 'check_delivery', content: 'arrives Tue — after Friday', delay: 1500 },
  { type: 'text', content: 'That misses Friday. Finding a faster route.', delay: 900 },
  { type: 'spawn', content: 'logistics-agent · fastest fulfillment by Fri', delay: 1500 },
  { type: 'thinking', delay: 1200 },
  { type: 'tool_call', tool: 'find_fulfillment', content: 'across warehouses', delay: 500 },
  { type: 'tool_result', tool: 'find_fulfillment', content: 'US-West: in stock, next-day → Thu', delay: 1500 },
  { type: 'spawn_done', content: 'logistics-agent: route found', delay: 1000 },
  { type: 'thinking', delay: 1200 },
  {
    type: 'response',
    content: '**Trailshell Lite — $139**, shipped next-day from US-West, **arrives Thursday** — beats your Friday deadline. Lock it in?',
    delay: 1800,
  },
  { type: 'ask_buttons', content: 'Ready to order?', options: ['Order it', 'See other options'], delay: 1600 },
  { type: 'button_click', content: 'Order it', delay: 1000 },
  { type: 'tool_call', tool: 'place_order', content: 'Trailshell Lite · next-day · US-West', delay: 400 },
  { type: 'tool_result', tool: 'place_order', content: 'order confirmed — arrives Thu', delay: 1300 },
  { type: 'text', content: 'Done — confirmed, arrives Thursday. Want trekking poles to match?', delay: 1500 },
];

const TYPEWRITER_TYPES = new Set<DemoStep['type']>(['input_typing', 'text', 'response', 'note']);
const USER_CHAR_MS = 55;
const AGENT_CHAR_MS = 12;

const BREW_PHRASES = ['Thinking...', 'Brewing...', 'Working...', 'Almost...'];
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

// Neutral spinner for the DIY/plain panel — no coffee mark, no "Brewing" (that
// branding belongs only to the SyntheticBrew panel).
function PlainSpinner() {
  return (
    <div className="demo-spin">
      <span className="demo-spin-dots">
        {[0, 0.15, 0.3].map((d) => (
          <span key={d} className="demo-spin-dot" style={{ animationDelay: `${d}s` }} />
        ))}
      </span>
      <span className="demo-spin-label demo-spin-label--steady">Thinking...</span>
    </div>
  );
}

function inlineBold(text: string): ReactNode {
  const parts = text.split(/\*\*(.+?)\*\*/g);
  if (parts.length === 1) return text;
  return parts.map((p, i) => (i % 2 === 1 ? <strong key={i}>{p}</strong> : p));
}

function renderMarkdown(raw: string): ReactNode[] {
  return raw.split('\n').map((line, i) => (line === '' ? <br key={i} /> : <div key={i}>{inlineBold(line)}</div>));
}

function UserBubble({ text, grounded }: { text: string; grounded?: boolean }) {
  return (
    <div className="demo-bubble-row">
      <div className={`demo-bubble${grounded ? '' : ' demo-bubble--plain'}`}>{text}</div>
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

function SpawnBlock({ content }: { content: string }) {
  return <div className="demo-spawn">↳ {content}</div>;
}

function SpawnDoneBlock({ content }: { content: string }) {
  return <div className="demo-spawn demo-spawn--done">✓ {content}</div>;
}

function ProductCardsBlock({ products }: { products: Product[] }) {
  return (
    <div className="demo-products">
      {products.map((p) => (
        <div key={p.name} className={`demo-product${p.pick ? ' is-pick' : ''}`}>
          <div className="demo-product-img">
            {p.img && <img src={p.img} alt={p.name} loading="lazy" onError={(e) => { (e.currentTarget as HTMLImageElement).style.display = 'none'; }} />}
          </div>
          <div className="demo-product-titlerow">
            <span className="demo-product-name">{p.name}</span>
            {p.pick && <span className="demo-pick-badge">pick</span>}
          </div>
          <span className="demo-product-price">{p.price}</span>
          <div className="demo-product-tags">
            {p.tags.map((t) => (
              <span key={t} className="demo-product-tag">{t}</span>
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}

function AgentText({ text, isResponse }: { text: string; isResponse?: boolean }) {
  return <div className="demo-msg">{isResponse ? renderMarkdown(text) : text}</div>;
}

function NoteText({ text }: { text: string }) {
  return <div className="demo-note">{text}</div>;
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
            <span key={opt} className={`demo-chip${isSelected ? ' is-selected' : ''}${isFaded ? ' is-faded' : ''}`}>{opt}</span>
          );
        })}
      </div>
    </div>
  );
}

interface PanelState {
  steps: ReactNode[];
  inputDisplay: string;
  done: boolean;
  bodyRef: RefObject<HTMLDivElement | null>;
}

/* Per-panel animation, reset by `cycle`, paused by `paused`. Holds at end. */
function usePanel(scenario: DemoStep[], paused: boolean, cycle: number, variant: Variant): PanelState {
  const [visibleSteps, setVisibleSteps] = useState(0);
  const [typingIndex, setTypingIndex] = useState(-1);
  const [typedChars, setTypedChars] = useState(0);
  const [selectedButton, setSelectedButton] = useState<string | undefined>();
  const [inputText, setInputText] = useState('');
  const bodyRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    setVisibleSteps(0); setTypingIndex(-1); setTypedChars(0); setSelectedButton(undefined); setInputText('');
  }, [cycle]);

  const done = visibleSteps >= scenario.length;

  useEffect(() => {
    if (paused || done) return;
    const step = scenario[visibleSteps];

    if (step.type === 'input_typing' && step.content) {
      if (typingIndex !== visibleSteps) { setTypingIndex(visibleSteps); setTypedChars(0); setInputText(''); return; }
      if (typedChars < step.content.length) {
        const t = setTimeout(() => { setTypedChars((c) => c + 1); setInputText(step.content!.slice(0, typedChars + 1)); }, USER_CHAR_MS);
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
      if (typedChars < step.content.length) { const t = setTimeout(() => setTypedChars((c) => c + 1), AGENT_CHAR_MS); return () => clearTimeout(t); }
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
  }, [visibleSteps, paused, typingIndex, typedChars, scenario, done]);

  useEffect(() => {
    bodyRef.current?.scrollTo({ top: bodyRef.current.scrollHeight, behavior: 'smooth' });
  }, [visibleSteps, typedChars]);

  const getUserTextForSend = useCallback((sendIndex: number) => {
    for (let j = sendIndex - 1; j >= 0; j--) { if (scenario[j].type === 'input_typing') return scenario[j].content ?? ''; }
    return '';
  }, [scenario]);

  const steps = useMemo((): ReactNode[] => {
    const elements: ReactNode[] = [];
    for (let i = 0; i < visibleSteps; i++) {
      const step = scenario[i];
      const key = `s-${i}`;
      const text = step.content ?? '';
      switch (step.type) {
        case 'input_typing': break;
        case 'input_send': elements.push(<UserBubble key={key} text={getUserTextForSend(i)} grounded={variant === 'grounded'} />); break;
        case 'thinking': break;
        case 'tool_call': {
          const next = i + 1 < visibleSteps ? scenario[i + 1] : null;
          if (next?.type === 'tool_result') break;
          elements.push(<ToolCallBlock key={key} tool={step.tool ?? ''} args={text} />);
          break;
        }
        case 'tool_result': {
          let callArgs = '';
          for (let j = i - 1; j >= 0; j--) { if (scenario[j].type === 'tool_call') { callArgs = scenario[j].content ?? ''; break; } }
          elements.push(<ToolCallBlock key={key} tool={step.tool ?? ''} args={callArgs} result={text} done />);
          break;
        }
        case 'text': elements.push(<AgentText key={key} text={text} />); break;
        case 'note': elements.push(<NoteText key={key} text={text} />); break;
        case 'response': elements.push(<AgentText key={key} text={text} isResponse />); break;
        case 'spawn': elements.push(<SpawnBlock key={key} content={text} />); break;
        case 'spawn_done': elements.push(<SpawnDoneBlock key={key} content={text} />); break;
        case 'product_cards': if (step.products) elements.push(<ProductCardsBlock key={key} products={step.products} />); break;
        case 'ask_buttons': elements.push(<AskButtons key={key} content={text} options={step.options ?? []} selected={selectedButton} />); break;
        case 'button_click': break;
      }
    }
    if (typingIndex >= 0 && typingIndex === visibleSteps && typingIndex < scenario.length) {
      const step = scenario[typingIndex];
      if (step.type !== 'input_typing') {
        const partial = (step.content ?? '').slice(0, typedChars);
        if (step.type === 'note') elements.push(<NoteText key={`t-${typingIndex}`} text={partial + '▌'} />);
        else elements.push(<AgentText key={`t-${typingIndex}`} text={partial + '▌'} isResponse={step.type === 'response'} />);
      }
    }
    if (visibleSteps < scenario.length && scenario[visibleSteps].type === 'thinking' && typingIndex < 0) {
      elements.push(variant === 'grounded' ? <BrewingSpinner key={`b-${visibleSteps}`} /> : <PlainSpinner key={`b-${visibleSteps}`} />);
    }
    return elements;
  }, [visibleSteps, typingIndex, typedChars, selectedButton, getUserTextForSend, scenario, variant]);

  const inputDisplay = useMemo(() => {
    if (typingIndex >= 0 && typingIndex < scenario.length && scenario[typingIndex].type === 'input_typing') return inputText + '▌';
    return '';
  }, [typingIndex, inputText, scenario]);

  return { steps, inputDisplay, done, bodyRef };
}

function Panel({ variant, panel }: { variant: Variant; panel: PanelState }) {
  const grounded = variant === 'grounded';
  const isInputActive = panel.inputDisplay.length > 0;
  const label = grounded
    ? <>☕ SyntheticBrew <span className="demo-dim">&middot; grounded agent</span></>
    : <>DIY agent <span className="demo-dim">&middot; ungrounded, untuned</span></>;
  return (
    <div className={`demo-panel ${grounded ? 'demo-panel--grounded' : 'demo-panel--plain'}`}>
      {grounded && <div className="demo-panel-strip" />}
      <div className="demo-titlebar">
        {grounded ? (
          <>
            <div className="demo-dot demo-dot--red" />
            <div className="demo-dot demo-dot--yellow" />
            <div className="demo-dot demo-dot--green" />
          </>
        ) : (
          <>
            <div className="demo-dot demo-dot--dim" />
            <div className="demo-dot demo-dot--dim" />
            <div className="demo-dot demo-dot--dim" />
          </>
        )}
        <span className="demo-panel-title">{label}</span>
      </div>
      <div ref={panel.bodyRef} className="demo-body demo-body--panel">
        {panel.steps}
      </div>
      <div className="demo-inputbar">
        <div className={`demo-input${isInputActive ? ' is-typing' : ''}${isInputActive && grounded ? ' is-accent' : ''}`}>
          {isInputActive ? panel.inputDisplay : 'Type a message...'}
        </div>
        <button className={`demo-send${grounded ? '' : ' demo-send--dim'}`} tabIndex={-1}>Send</button>
      </div>
    </div>
  );
}

export default function ProductDemo() {
  const [cycle, setCycle] = useState(0);
  const [paused, setPaused] = useState(false);
  const plain = usePanel(PLAIN_SCENARIO, paused, cycle, 'plain');
  const grounded = usePanel(GROUNDED_SCENARIO, paused, cycle, 'grounded');

  // Restart only when BOTH have finished — keeps the two panels in lock-step.
  useEffect(() => {
    if (paused || !plain.done || !grounded.done) return;
    const t = setTimeout(() => setCycle((c) => c + 1), 30000);
    return () => clearTimeout(t);
  }, [paused, plain.done, grounded.done]);

  return (
    <div className="demo-compare" onMouseEnter={() => setPaused(true)} onMouseLeave={() => setPaused(false)}>
      <p className="demo-compare-kicker">Same question, side by side &mdash; a live comparison</p>
      <div className="demo-compare-grid">
        <div>
          <div className="demo-compare-title demo-compare-title--plain">DIY agent</div>
          <Panel variant="plain" panel={plain} />
          <p className="demo-compare-caption">A quick DIY build &mdash; guesses, can&rsquo;t check stock, delivery, or close.</p>
        </div>
        <div>
          <div className="demo-compare-title demo-compare-title--brew">SyntheticBrew</div>
          <Panel variant="grounded" panel={grounded} />
          <p className="demo-compare-caption demo-compare-caption--brew">Consults, solves the delivery, and closes the sale.</p>
        </div>
        <span className="demo-vs">VS</span>
      </div>
    </div>
  );
}
