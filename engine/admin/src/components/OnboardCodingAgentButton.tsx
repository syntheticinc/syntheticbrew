import { useRef, useState } from 'react';
import { api } from '../api/client';

/* One-click onboarding (mirrors the hero pattern): the click mints a scoped
   provision token once and copies a fully self-contained instruction — engine
   URL + token + the setup task — that the user pastes into any MCP-capable
   coding agent. Works offline/self-hosted: the MCP endpoint is this engine. */

// Hero pattern: a short fetch-instruction — the engine itself serves the full
// validated steps at /agent-setup/prompt.md, so they always match the engine
// version. The token rides along because CE/EE MCP auth is a Bearer token.
function buildPrompt(origin: string, token: string): string {
  return `Fetch ${origin}/agent-setup/prompt.md and follow the instructions. My access token: ${token}`;
}

interface OnboardCodingAgentButtonProps {
  compact?: boolean;
}

export default function OnboardCodingAgentButton({ compact = false }: OnboardCodingAgentButtonProps) {
  const [state, setState] = useState<'idle' | 'minting' | 'copied' | 'error'>('idle');
  // One token per page visit — repeat clicks re-copy the same instruction.
  const tokenRef = useRef<string | null>(null);

  async function handleClick() {
    if (state === 'minting') return;
    setState('minting');
    try {
      if (!tokenRef.current) {
        const res = await api.createToken({
          name: `coding-agent-${new Date().toISOString().slice(0, 10)}`,
          scopes: ['provision'],
        });
        tokenRef.current = res.token;
      }
      const prompt = buildPrompt(window.location.origin, tokenRef.current);
      let ok = false;
      try {
        await navigator.clipboard.writeText(prompt);
        ok = true;
      } catch {
        // Clipboard API denied — legacy fallback.
        const ta = document.createElement('textarea');
        ta.value = prompt;
        ta.style.position = 'fixed';
        ta.style.opacity = '0';
        document.body.appendChild(ta);
        ta.select();
        try {
          ok = document.execCommand('copy');
        } finally {
          ta.remove();
        }
      }
      setState(ok ? 'copied' : 'error');
    } catch {
      setState('error');
    }
    setTimeout(() => setState('idle'), 3000);
  }

  const label =
    state === 'copied'
      ? 'Copied ✓ — paste it into your agent'
      : state === 'minting'
        ? 'Preparing…'
        : state === 'error'
          ? 'Failed — try again'
          : 'Onboard a coding agent';

  if (compact) {
    return (
      <button
        type="button"
        onClick={handleClick}
        className="text-[11px] text-brand-shade2 hover:text-brand-light border border-brand-shade3/30 rounded-btn px-2.5 py-1 transition-colors cursor-pointer"
        data-testid="topbar-connect-agent"
      >
        {state === 'copied' ? 'Copied ✓ — paste into your agent' : 'Connect coding agent'}
      </button>
    );
  }

  return (
    <button
      type="button"
      onClick={handleClick}
      className="px-4 py-2 bg-brand-accent text-white rounded-btn text-sm font-medium hover:bg-brand-accent-hover transition-colors cursor-pointer disabled:opacity-50"
      data-testid="agents-empty-connect-agent"
    >
      {label}
    </button>
  );
}
