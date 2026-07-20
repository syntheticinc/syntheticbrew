import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import OnboardCodingAgentButton from './OnboardCodingAgentButton';

vi.mock('../api/client', () => ({
  api: { createToken: vi.fn() },
}));

import { api } from '../api/client';
const mockApi = vi.mocked(api);

beforeEach(() => {
  vi.clearAllMocks();
  Object.assign(navigator, {
    clipboard: { writeText: vi.fn().mockResolvedValue(undefined) },
  });
});

describe('OnboardCodingAgentButton', () => {
  it('mints a provision token and copies a self-contained instruction', async () => {
    mockApi.createToken.mockResolvedValue({ id: '1', name: 'coding-agent', token: 'bb_onboard_1' });

    render(<OnboardCodingAgentButton />);
    await userEvent.click(screen.getByTestId('agents-empty-connect-agent'));

    await waitFor(() =>
      expect(screen.getByTestId('agents-empty-connect-agent')).toHaveTextContent('Copied ✓'),
    );

    expect(mockApi.createToken).toHaveBeenCalledWith({
      name: expect.stringMatching(/^coding-agent-\d{4}-\d{2}-\d{2}$/),
      scopes: ['provision'],
    });
    const prompt = vi.mocked(navigator.clipboard.writeText).mock.calls[0]![0] as string;
    expect(prompt).toContain(`${window.location.origin}/api/v1/mcp/rpc`);
    expect(prompt).toContain('Bearer bb_onboard_1');
    expect(prompt).toContain('knowledge base');
    expect(prompt).toContain('embed snippet');
    // Self-contained: no URLs beyond this engine's own origin.
    const external = prompt.match(/https?:\/\/[^\s"]+/g)!.filter(
      (u) => !u.startsWith(window.location.origin),
    );
    expect(external).toEqual([]);
  });

  it('reuses the minted token on repeat clicks', async () => {
    mockApi.createToken.mockResolvedValue({ id: '1', name: 'coding-agent', token: 'bb_once' });

    render(<OnboardCodingAgentButton />);
    await userEvent.click(screen.getByTestId('agents-empty-connect-agent'));
    await waitFor(() =>
      expect(screen.getByTestId('agents-empty-connect-agent')).toHaveTextContent('Copied ✓'),
    );
    await userEvent.click(screen.getByTestId('agents-empty-connect-agent'));

    expect(mockApi.createToken).toHaveBeenCalledTimes(1);
  });

  it('renders the compact top-bar variant', async () => {
    mockApi.createToken.mockResolvedValue({ id: '1', name: 'coding-agent', token: 'bb_c' });

    render(<OnboardCodingAgentButton compact />);
    const btn = screen.getByTestId('topbar-connect-agent');
    expect(btn).toHaveTextContent('Connect coding agent');
    await userEvent.click(btn);
    await waitFor(() => expect(btn).toHaveTextContent('Copied ✓'));
  });

  it('shows an error state when minting fails', async () => {
    mockApi.createToken.mockRejectedValue(new Error('boom'));

    render(<OnboardCodingAgentButton />);
    await userEvent.click(screen.getByTestId('agents-empty-connect-agent'));

    await waitFor(() =>
      expect(screen.getByTestId('agents-empty-connect-agent')).toHaveTextContent('Failed'),
    );
  });
});
