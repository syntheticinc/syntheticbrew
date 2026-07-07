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

beforeEach(() => {
  vi.clearAllMocks();
  Object.assign(navigator, {
    clipboard: { writeText: vi.fn().mockResolvedValue(undefined) },
  });
});

describe('ConnectClaudeCode', () => {
  it('renders the connect button and destructive-ops checkbox (off by default)', () => {
    render(<ConnectClaudeCode />);
    expect(screen.getByRole('button', { name: 'Connect Claude Code' })).toBeInTheDocument();
    const checkbox = screen.getByRole('checkbox');
    expect(checkbox).not.toBeChecked();
  });

  it('mints with ["provision"] by default and shows the token + config blocks once', async () => {
    mockApi.createToken.mockResolvedValue({
      id: '1',
      name: 'claude-code',
      token: 'bb_test_token_123',
    });

    render(<ConnectClaudeCode />);
    await userEvent.click(screen.getByRole('button', { name: 'Connect Claude Code' }));

    await waitFor(() => {
      expect(screen.getByDisplayValue('bb_test_token_123')).toBeInTheDocument();
    });

    // Default scopes: provision only.
    expect(mockApi.createToken).toHaveBeenCalledTimes(1);
    const arg = mockApi.createToken.mock.calls[0]![0];
    expect(arg.scopes).toEqual(['provision']);

    // Show-once warning + both config snippets are present.
    expect(screen.getByText(/shown once/i)).toBeInTheDocument();
    expect(screen.getByText(/claude mcp add --transport http syntheticbrew/)).toBeInTheDocument();
    expect(screen.getByText(/"mcpServers"/)).toBeInTheDocument();
    // The MCP RPC path appears in the intro copy plus both snippet blocks.
    expect(screen.getAllByText(/\/api\/v1\/mcp\/rpc/).length).toBeGreaterThanOrEqual(2);
  });

  it('includes "manage" scope when destructive operations are allowed', async () => {
    mockApi.createToken.mockResolvedValue({
      id: '2',
      name: 'claude-code',
      token: 'bb_test_token_456',
    });

    render(<ConnectClaudeCode />);
    await userEvent.click(screen.getByRole('checkbox'));
    await userEvent.click(screen.getByRole('button', { name: 'Connect Claude Code' }));

    await waitFor(() => {
      expect(screen.getByDisplayValue('bb_test_token_456')).toBeInTheDocument();
    });

    const arg = mockApi.createToken.mock.calls[0]![0];
    expect(arg.scopes).toEqual(['provision', 'manage']);
  });

  it('calls onMinted after a successful mint', async () => {
    mockApi.createToken.mockResolvedValue({ id: '3', name: 'claude-code', token: 'bb_x' });
    const onMinted = vi.fn();

    render(<ConnectClaudeCode onMinted={onMinted} />);
    await userEvent.click(screen.getByRole('button', { name: 'Connect Claude Code' }));

    await waitFor(() => expect(onMinted).toHaveBeenCalledTimes(1));
  });

  it('surfaces an error when minting fails', async () => {
    mockApi.createToken.mockRejectedValue(new Error('boom'));

    render(<ConnectClaudeCode />);
    await userEvent.click(screen.getByRole('button', { name: 'Connect Claude Code' }));

    await waitFor(() => {
      expect(screen.getByText('boom')).toBeInTheDocument();
    });
  });
});
