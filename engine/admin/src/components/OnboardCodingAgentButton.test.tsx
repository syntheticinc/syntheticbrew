import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import OnboardCodingAgentButton from './OnboardCodingAgentButton';

// The whole security point of Stage 3: no token is minted or embedded. Guard
// that the component never touches createToken.
const createToken = vi.fn();
vi.mock('../api/client', () => ({
  api: { createToken },
}));

function renderButton(props?: { compact?: boolean }) {
  return render(
    <MemoryRouter>
      <OnboardCodingAgentButton {...props} />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  Object.assign(navigator, {
    clipboard: { writeText: vi.fn().mockResolvedValue(undefined) },
  });
});

describe('OnboardCodingAgentButton', () => {
  it('copies a token-free fetch-prompt line and never mints a token', async () => {
    renderButton();
    await userEvent.click(screen.getByTestId('agents-empty-connect-agent'));

    await waitFor(() =>
      expect(screen.getByTestId('agents-empty-connect-agent')).toHaveTextContent('Copied ✓'),
    );

    // No token minting — the agent connects via OAuth, not an embedded secret.
    expect(createToken).not.toHaveBeenCalled();

    const prompt = vi.mocked(navigator.clipboard.writeText).mock.calls[0]![0] as string;
    expect(prompt).toBe(
      `Fetch ${window.location.origin}/agent-setup/prompt.md and follow the instructions.`,
    );
    // No access token leaks into the copied text.
    expect(prompt).not.toMatch(/access token/i);
    expect(prompt).not.toMatch(/bb_/);
    // Self-contained: no URLs beyond this engine's own origin.
    const external = (prompt.match(/https?:\/\/[^\s"]+/g) ?? []).filter(
      (u) => !u.startsWith(window.location.origin),
    );
    expect(external).toEqual([]);
  });

  it('links to the API Keys page for the manual key path', () => {
    renderButton();
    const link = screen.getByTestId('onboard-apikeys-link');
    expect(link).toHaveAttribute('href', '/api-keys');
  });

  it('renders the compact top-bar variant and copies the same line', async () => {
    renderButton({ compact: true });
    const btn = screen.getByTestId('topbar-connect-agent');
    expect(btn).toHaveTextContent('Connect coding agent');

    await userEvent.click(btn);
    await waitFor(() => expect(btn).toHaveTextContent('Copied ✓'));

    expect(createToken).not.toHaveBeenCalled();
    const prompt = vi.mocked(navigator.clipboard.writeText).mock.calls[0]![0] as string;
    expect(prompt).toBe(
      `Fetch ${window.location.origin}/agent-setup/prompt.md and follow the instructions.`,
    );
  });

  it('shows an error state when the clipboard copy fails', async () => {
    vi.mocked(navigator.clipboard.writeText).mockRejectedValue(new Error('denied'));
    // Legacy fallback also fails.
    document.execCommand = vi.fn().mockReturnValue(false);

    renderButton();
    await userEvent.click(screen.getByTestId('agents-empty-connect-agent'));

    await waitFor(() =>
      expect(screen.getByTestId('agents-empty-connect-agent')).toHaveTextContent('Failed'),
    );
  });
});
