import { useState } from 'react';
import { Link } from 'react-router-dom';

/* One-click onboarding (mirrors the hero pattern): the click copies a short,
   token-free instruction the user pastes into any MCP-capable coding agent.
   No access token is embedded — the agent OAuth-connects via discovery and the
   user approves it on the consent page, so a secret never lands in the agent
   chat. The manual API-key path stays under API Keys for headless/CI use. */

// The engine serves the full validated steps at /agent-setup/prompt.md, so they
// always match the running engine version.
function buildPrompt(origin: string): string {
  return `Fetch ${origin}/agent-setup/prompt.md and follow the instructions.`;
}

async function copyToClipboard(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    // Clipboard API denied — legacy fallback.
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.select();
    try {
      return document.execCommand('copy');
    } finally {
      ta.remove();
    }
  }
}

interface OnboardCodingAgentButtonProps {
  compact?: boolean;
}

export default function OnboardCodingAgentButton({ compact = false }: OnboardCodingAgentButtonProps) {
  const [state, setState] = useState<'idle' | 'copied' | 'error'>('idle');

  async function handleClick() {
    const ok = await copyToClipboard(buildPrompt(window.location.origin));
    setState(ok ? 'copied' : 'error');
    setTimeout(() => setState('idle'), 3000);
  }

  const label =
    state === 'copied'
      ? 'Copied ✓ — paste it into your agent'
      : state === 'error'
        ? 'Failed — try again'
        : 'Onboard a coding agent';

  if (compact) {
    return (
      <button
        type="button"
        onClick={handleClick}
        title="Copies a setup line for a coding agent. No token is included — the agent connects via OAuth and you approve it on the consent screen."
        className="text-[11px] text-brand-shade2 hover:text-brand-light border border-brand-shade3/30 rounded-btn px-2.5 py-1 transition-colors cursor-pointer"
        data-testid="topbar-connect-agent"
      >
        {state === 'copied' ? 'Copied ✓ — paste into your agent' : 'Connect coding agent'}
      </button>
    );
  }

  return (
    <span className="inline-flex flex-col items-center gap-1.5">
      <button
        type="button"
        onClick={handleClick}
        className="px-4 py-2 bg-brand-accent text-white rounded-btn text-sm font-medium hover:bg-brand-accent-hover transition-colors cursor-pointer disabled:opacity-50"
        data-testid="agents-empty-connect-agent"
      >
        {label}
      </button>
      <span className="text-[11px] text-brand-shade3/80">
        No token to paste — connects via OAuth.{' '}
        <Link
          to="/api-keys"
          className="text-brand-accent hover:underline"
          data-testid="onboard-apikeys-link"
        >
          Prefer an API key?
        </Link>
      </span>
    </span>
  );
}
