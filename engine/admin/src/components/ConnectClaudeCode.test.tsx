import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import ConnectClaudeCode from './ConnectClaudeCode';

vi.mock('../api/client', () => ({
  api: {
    createToken: vi.fn(),
  },
}));

import { api } from '../api/client';
const mockApi = vi.mocked(api);

const ALL_TABS = ['Claude Code', 'Cursor', 'VS Code', 'Codex', 'JSON'];

beforeEach(() => {
  vi.clearAllMocks();
  Object.assign(navigator, {
    clipboard: { writeText: vi.fn().mockResolvedValue(undefined) },
  });
});

async function mintToken(token: string) {
  mockApi.createToken.mockResolvedValue({ id: '1', name: 'coding-agent', token });
  render(<ConnectClaudeCode />);
  await userEvent.click(screen.getByRole('button', { name: 'Generate connection token' }));
  await waitFor(() => {
    expect(screen.getByDisplayValue(token)).toBeInTheDocument();
  });
}

describe('ConnectClaudeCode', () => {
  it('renders the generate button and destructive-ops checkbox (off by default)', () => {
    render(<ConnectClaudeCode />);
    expect(screen.getByText('Connect a coding agent')).toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: 'Generate connection token' }),
    ).toBeInTheDocument();
    const checkbox = screen.getByRole('checkbox');
    expect(checkbox).not.toBeChecked();
  });

  it('mints with ["provision"] by default and shows the token + setup snippet once', async () => {
    await mintToken('bb_test_token_123');

    // Default scopes: provision only.
    expect(mockApi.createToken).toHaveBeenCalledTimes(1);
    const arg = mockApi.createToken.mock.calls[0]![0];
    expect(arg.scopes).toEqual(['provision']);

    // Show-once warning + the default (Claude Code) snippet are present.
    expect(screen.getByText(/shown once/i)).toBeInTheDocument();
    expect(screen.getByText(/claude mcp add --transport http syntheticbrew/)).toBeInTheDocument();
    // The MCP RPC path appears in the intro copy plus the snippet block.
    expect(screen.getAllByText(/\/api\/v1\/mcp\/rpc/).length).toBeGreaterThanOrEqual(2);
  });

  it('includes "manage" scope when destructive operations are allowed', async () => {
    mockApi.createToken.mockResolvedValue({
      id: '2',
      name: 'coding-agent',
      token: 'bb_test_token_456',
    });

    render(<ConnectClaudeCode />);
    await userEvent.click(screen.getByRole('checkbox'));
    await userEvent.click(screen.getByRole('button', { name: 'Generate connection token' }));

    await waitFor(() => {
      expect(screen.getByDisplayValue('bb_test_token_456')).toBeInTheDocument();
    });

    const arg = mockApi.createToken.mock.calls[0]![0];
    expect(arg.scopes).toEqual(['provision', 'manage']);
  });

  it('renders all five agent tabs after mint', async () => {
    await mintToken('bb_tabs_token');

    for (const name of ALL_TABS) {
      expect(screen.getByRole('tab', { name })).toBeInTheDocument();
    }
    // Claude Code is the default active tab.
    expect(screen.getByRole('tab', { name: 'Claude Code' })).toHaveAttribute(
      'aria-selected',
      'true',
    );
  });

  it('every tab snippet contains the minted token and the MCP RPC URL', async () => {
    await mintToken('bb_per_tab_token_789');

    for (const name of ALL_TABS) {
      await userEvent.click(screen.getByRole('tab', { name }));
      const snippet = screen.getByTestId('connect-snippet');
      expect(snippet.textContent).toContain('bb_per_tab_token_789');
      expect(snippet.textContent).toContain('/api/v1/mcp/rpc');
    }
  });

  it('Codex tab shows the env-var export and --bearer-token-env-var flag', async () => {
    await mintToken('bb_codex_token');

    await userEvent.click(screen.getByRole('tab', { name: 'Codex' }));
    const snippet = screen.getByTestId('connect-snippet');
    expect(snippet.textContent).toContain('export SYNTHETICBREW_TOKEN=bb_codex_token');
    expect(snippet.textContent).toContain('--bearer-token-env-var SYNTHETICBREW_TOKEN');
  });

  it('JSON tab shows the mcpServers config block', async () => {
    await mintToken('bb_json_token');

    await userEvent.click(screen.getByRole('tab', { name: 'JSON' }));
    const snippet = screen.getByTestId('connect-snippet');
    expect(snippet.textContent).toContain('"mcpServers"');
    expect(snippet.textContent).toContain('Bearer bb_json_token');
  });

  it('shows the self-contained setup prompt after mint', async () => {
    await mintToken('bb_prompt_token');

    const prompt = screen.getByTestId('setup-prompt-snippet');
    expect(prompt.textContent).toContain('Set up my SyntheticBrew agent end-to-end');
    expect(prompt.textContent).toContain('knowledge base');
    expect(prompt.textContent).toContain('embed snippet');
    // Self-contained: no external URLs in the prompt (CE stays origin-only).
    expect(prompt.textContent).not.toMatch(/https?:\/\//);
  });

  it('calls onMinted after a successful mint', async () => {
    mockApi.createToken.mockResolvedValue({ id: '3', name: 'coding-agent', token: 'bb_x' });
    const onMinted = vi.fn();

    render(<ConnectClaudeCode onMinted={onMinted} />);
    await userEvent.click(screen.getByRole('button', { name: 'Generate connection token' }));

    await waitFor(() => expect(onMinted).toHaveBeenCalledTimes(1));
  });

  it('surfaces an error when minting fails', async () => {
    mockApi.createToken.mockRejectedValue(new Error('boom'));

    render(<ConnectClaudeCode />);
    await userEvent.click(screen.getByRole('button', { name: 'Generate connection token' }));

    await waitFor(() => {
      expect(screen.getByText('boom')).toBeInTheDocument();
    });
  });
});
