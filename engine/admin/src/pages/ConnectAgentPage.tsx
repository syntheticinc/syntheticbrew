import ConnectClaudeCode from '../components/ConnectClaudeCode';
import PageContainer from '../components/PageContainer';

/* Dedicated onboarding surface: connecting a coding agent is an activation
   flow, not key management — the scoped token it mints is an implementation
   detail (it still shows up on the API Keys page for revocation). */
export default function ConnectAgentPage() {
  return (
    <PageContainer>
      <div className="mb-6">
        <h1 className="text-2xl font-bold text-brand-light">Connect a coding agent</h1>
        <p className="text-sm text-brand-shade3 mt-1 max-w-2xl">
          Let Cursor, Claude Code, or any MCP-capable agent configure this
          engine for you — it can create agents, load knowledge, and hand you
          an embed snippet.
        </p>
      </div>
      <ConnectClaudeCode />
    </PageContainer>
  );
}
