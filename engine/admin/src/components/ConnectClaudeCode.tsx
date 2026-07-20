import { useState } from 'react';
import { api } from '../api/client';

// engineUrl is the base the SPA is served from. The admin SPA is served by the
// engine itself, so window.location.origin is the address a coding agent on the
// same host/network uses to reach POST /api/v1/mcp/rpc.
function engineUrl(): string {
  if (typeof window !== 'undefined' && window.location?.origin) {
    return window.location.origin;
  }
  return 'https://your-engine.example.com';
}

const MCP_RPC_PATH = '/api/v1/mcp/rpc';

// The engine serves the full, validated setup steps at /agent-setup/prompt.md,
// so the copyable instruction points there instead of duplicating the steps —
// one source of truth shared with the OAuth onboarding flow.
function setupPrompt(url: string): string {
  return `Fetch ${url}/agent-setup/prompt.md and follow the instructions.`;
}

function claudeCodeCommand(url: string, token: string): string {
  return `claude mcp add --transport http syntheticbrew ${url}${MCP_RPC_PATH} --header "Authorization: Bearer ${token}"`;
}

function mcpJson(url: string, token: string): string {
  return JSON.stringify(
    {
      mcpServers: {
        syntheticbrew: {
          type: 'http',
          url: `${url}${MCP_RPC_PATH}`,
          headers: { Authorization: `Bearer ${token}` },
        },
      },
    },
    null,
    2,
  );
}

function vsCodeCommand(url: string, token: string): string {
  const config = JSON.stringify({
    name: 'syntheticbrew',
    type: 'http',
    url: `${url}${MCP_RPC_PATH}`,
    headers: { Authorization: `Bearer ${token}` },
  });
  return `code --add-mcp '${config}'`;
}

function codexCommands(url: string, token: string): string {
  return [
    `export SYNTHETICBREW_TOKEN=${token}`,
    `codex mcp add syntheticbrew --url ${url}${MCP_RPC_PATH} --bearer-token-env-var SYNTHETICBREW_TOKEN`,
  ].join('\n');
}

interface AgentSnippet {
  id: string;
  tabLabel: string;
  blockLabel: string;
  build: (url: string, token: string) => string;
}

// Adding support for a new coding agent = adding a row here.
const AGENT_SNIPPETS: readonly AgentSnippet[] = [
  {
    id: 'claude-code',
    tabLabel: 'Claude Code',
    blockLabel: 'Claude Code (CLI)',
    build: claudeCodeCommand,
  },
  {
    id: 'cursor',
    tabLabel: 'Cursor',
    blockLabel: 'Cursor — add to ~/.cursor/mcp.json (or project .cursor/mcp.json)',
    build: mcpJson,
  },
  {
    id: 'vscode',
    tabLabel: 'VS Code',
    blockLabel: 'VS Code (CLI)',
    build: vsCodeCommand,
  },
  {
    id: 'codex',
    tabLabel: 'Codex',
    blockLabel: 'OpenAI Codex (CLI)',
    build: codexCommands,
  },
  {
    id: 'json',
    tabLabel: 'JSON',
    blockLabel: 'mcpServers config (any MCP client)',
    build: mcpJson,
  },
];

// CopyBlock renders a monospace snippet with a copy button, matching the
// snippet-output styling used on WidgetConfigPage and the token modal.
function CopyBlock({
  label,
  value,
  testId = 'connect-snippet',
}: {
  label: string;
  value: string;
  testId?: string;
}) {
  const [copied, setCopied] = useState(false);

  function handleCopy() {
    navigator.clipboard
      .writeText(value)
      .then(() => {
        setCopied(true);
        setTimeout(() => setCopied(false), 1500);
      })
      .catch(() => {
        /* clipboard unavailable — value is still selectable */
      });
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-2">
        <span className="text-xs font-semibold text-brand-shade3 uppercase tracking-widest font-mono">
          {label}
        </span>
        <button
          type="button"
          onClick={handleCopy}
          className="px-3 py-1.5 bg-brand-dark border border-brand-shade3/30 text-brand-shade2 hover:text-brand-light rounded-btn text-xs font-medium transition-colors cursor-pointer"
        >
          {copied ? 'Copied' : 'Copy'}
        </button>
      </div>
      <pre
        data-testid={testId}
        className="bg-brand-dark-alt px-4 py-3 rounded-card text-xs text-brand-shade2 font-mono overflow-x-auto border border-brand-shade3/20 whitespace-pre-wrap break-all"
      >
        <code>{value}</code>
      </pre>
    </div>
  );
}

interface ConnectClaudeCodeProps {
  // onMinted lets the parent refresh its token list after a successful mint.
  onMinted?: () => void;
}

export default function ConnectClaudeCode({ onMinted }: ConnectClaudeCodeProps) {
  const [allowManage, setAllowManage] = useState(false);
  const [minting, setMinting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [token, setToken] = useState<string | null>(null);
  const [tokenCopied, setTokenCopied] = useState(false);
  const [activeTabId, setActiveTabId] = useState<string>(AGENT_SNIPPETS[0]!.id);

  const url = engineUrl();
  const activeSnippet = AGENT_SNIPPETS.find((a) => a.id === activeTabId) ?? AGENT_SNIPPETS[0]!;

  async function handleConnect() {
    setMinting(true);
    setError(null);
    try {
      const scopes = allowManage ? ['provision', 'manage'] : ['provision'];
      const res = await api.createToken({
        name: `coding-agent-${new Date().toISOString().slice(0, 10)}`,
        scopes,
      });
      setToken(res.token);
      onMinted?.();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to mint token');
    } finally {
      setMinting(false);
    }
  }

  function copyToken() {
    if (!token) return;
    navigator.clipboard
      .writeText(token)
      .then(() => {
        setTokenCopied(true);
        setTimeout(() => setTokenCopied(false), 1500);
      })
      .catch(() => {
        /* clipboard unavailable — value is still selectable */
      });
  }

  function reset() {
    setToken(null);
    setAllowManage(false);
    setError(null);
    setActiveTabId(AGENT_SNIPPETS[0]!.id);
  }

  return (
    <div className="bg-brand-dark-alt rounded-card border border-brand-shade3/15 p-5 mb-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h2 className="text-base font-semibold text-brand-light">
            Manual setup — headless / CI
          </h2>
          <p className="mt-1 text-sm text-brand-shade3 max-w-2xl">
            For environments without a browser (CI, headless servers): mint a scoped API token and
            paste it into the agent config. When a browser is available, prefer the one-click OAuth
            connect instead — no token to copy. The agent reaches the engine at{' '}
            <code className="text-brand-shade2">{MCP_RPC_PATH}</code>.
          </p>
        </div>
      </div>

      {token === null ? (
        <div className="mt-4 space-y-4">
          <label className="flex items-start gap-2 cursor-pointer select-none">
            <input
              type="checkbox"
              checked={allowManage}
              onChange={(e) => setAllowManage(e.target.checked)}
              className="mt-0.5 rounded border-brand-shade3/30 text-brand-accent focus:ring-brand-accent bg-brand-dark"
            />
            <span>
              <span className="text-sm text-brand-light">Allow destructive operations (manage)</span>
              <span className="block text-xs text-brand-shade3">
                Grants delete/overwrite operations in addition to the default read/provision
                access. Leave off unless the agent needs to remove or replace resources.
              </span>
            </span>
          </label>

          {error && (
            <div className="p-3 bg-red-500/10 border border-red-500/30 rounded-btn text-sm text-red-400">
              {error}
            </div>
          )}

          <button
            type="button"
            onClick={handleConnect}
            disabled={minting}
            className="px-4 py-2 bg-brand-accent text-white rounded-btn text-sm font-medium hover:bg-brand-accent-hover transition-colors disabled:opacity-50"
          >
            {minting ? 'Generating…' : 'Generate connection token'}
          </button>
        </div>
      ) : (
        <div className="mt-4 space-y-4">
          <div className="p-3 bg-yellow-500/10 border border-yellow-500/30 rounded-btn text-sm text-yellow-400">
            Copy this token now — it is shown once and cannot be retrieved later.
          </div>

          {/* Token */}
          <div>
            <div className="flex items-center justify-between mb-2">
              <span className="text-xs font-semibold text-brand-shade3 uppercase tracking-widest font-mono">
                Token
              </span>
              <span className="text-xs text-brand-shade3">
                Scopes: {allowManage ? 'provision, manage' : 'provision'}
              </span>
            </div>
            <div className="flex items-center gap-2">
              <input
                type="text"
                value={token}
                readOnly
                className="flex-1 px-3 py-2 border border-brand-shade3/30 rounded-btn text-sm font-mono bg-brand-dark text-brand-light"
              />
              <button
                type="button"
                onClick={copyToken}
                className="px-3 py-1.5 bg-brand-dark border border-brand-shade3/30 text-brand-shade2 hover:text-brand-light rounded-btn text-xs font-medium transition-colors cursor-pointer"
              >
                {tokenCopied ? 'Copied' : 'Copy'}
              </button>
            </div>
          </div>

          {/* Per-agent setup snippets */}
          <div>
            <div
              role="tablist"
              aria-label="Coding agent"
              className="inline-flex flex-wrap gap-1 bg-brand-dark rounded-btn p-1 border border-brand-shade3/20 mb-3"
            >
              {AGENT_SNIPPETS.map((agent) => (
                <button
                  key={agent.id}
                  type="button"
                  role="tab"
                  aria-selected={agent.id === activeSnippet.id}
                  onClick={() => setActiveTabId(agent.id)}
                  className={`px-3 py-1.5 rounded-btn text-xs font-medium transition-colors ${
                    agent.id === activeSnippet.id
                      ? 'bg-brand-accent text-white'
                      : 'text-brand-shade2 hover:text-brand-light'
                  }`}
                >
                  {agent.tabLabel}
                </button>
              ))}
            </div>
            <CopyBlock label={activeSnippet.blockLabel} value={activeSnippet.build(url, token)} />
          </div>

          {/* Onboarding prompt — points at the engine-served steps, so it never
              drifts from the OAuth flow's single source of truth. */}
          <div className="pt-4 border-t border-brand-shade3/15">
            <p className="text-sm text-brand-light mb-3">
              Then give the agent this instruction — it fetches the full setup
              steps from the engine and builds a grounded agent for you:
            </p>
            <CopyBlock label="Setup prompt" value={setupPrompt(url)} testId="setup-prompt-snippet" />
          </div>

          <button
            type="button"
            onClick={reset}
            className="px-4 py-2 text-sm text-brand-shade2 border border-brand-shade3/30 rounded-btn hover:bg-brand-dark hover:text-brand-light transition-colors"
          >
            Done
          </button>
        </div>
      )}
    </div>
  );
}
