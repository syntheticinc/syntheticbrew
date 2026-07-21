import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import OAuthConsentPage from './OAuthConsentPage';

vi.mock('../api/oauth', () => ({
  getAuthorizeInfo: vi.fn(),
  approveAuthorization: vi.fn(),
}));

import { getAuthorizeInfo, approveAuthorization } from '../api/oauth';
const mockInfo = vi.mocked(getAuthorizeInfo);
const mockApprove = vi.mocked(approveAuthorization);

const QUERY = new URLSearchParams({
  client_id: 'client-123',
  redirect_uri: 'https://agent.example.com/callback',
  scope: 'provision manage',
  state: 'st-abc',
  code_challenge: 'chal-xyz',
  code_challenge_method: 'S256',
  resource: 'https://engine.example.com',
}).toString();

const originalLocation = window.location;
let hrefValue = '';

function setLocation(search: string) {
  hrefValue = '';
  Object.defineProperty(window, 'location', {
    configurable: true,
    value: {
      search: `?${search}`,
      origin: 'http://localhost:3010',
      get href() {
        return hrefValue;
      },
      set href(v: string) {
        hrefValue = v;
      },
    },
  });
}

function renderPage() {
  return render(
    <MemoryRouter>
      <OAuthConsentPage />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  setLocation(QUERY);
});

afterEach(() => {
  Object.defineProperty(window, 'location', {
    configurable: true,
    value: originalLocation,
  });
});

describe('OAuthConsentPage', () => {
  it('renders the client name (marked unverified), redirect host and provision scope', async () => {
    mockInfo.mockResolvedValue({
      client_name: 'Claude Code',
      scopes: ['provision', 'manage'],
      redirect_uri_valid: true,
      consent_nonce: 'nonce-1',
    });

    renderPage();

    expect(await screen.findByTestId('consent-card')).toBeInTheDocument();
    expect(screen.getByTestId('client-name')).toHaveTextContent('Claude Code');
    // T12: the self-reported name must be flagged as unverified.
    expect(screen.getByTestId('client-unverified')).toHaveTextContent(/unverified/i);
    // T12/T14: the redirect destination host is shown prominently.
    expect(screen.getByTestId('redirect-host')).toHaveTextContent('agent.example.com');
    expect(screen.getByTestId('scope-provision')).toBeInTheDocument();
  });

  it('does not auto-submit — approve is only called on an explicit click (T11)', async () => {
    mockInfo.mockResolvedValue({
      client_name: 'Claude Code',
      scopes: ['provision'],
      redirect_uri_valid: true,
      consent_nonce: 'nonce-1',
    });

    renderPage();
    await screen.findByTestId('consent-card');

    expect(mockApprove).not.toHaveBeenCalled();
  });

  it('manage defaults to unchecked; Allow grants provision only and follows the redirect', async () => {
    mockInfo.mockResolvedValue({
      client_name: 'Claude Code',
      scopes: ['provision', 'manage'],
      redirect_uri_valid: true,
      consent_nonce: 'nonce-1',
    });
    mockApprove.mockResolvedValue({ redirect_url: 'https://agent.example.com/callback?code=abc' });

    renderPage();
    await screen.findByTestId('consent-card');

    // T12: the destructive-scope checkbox is present and off by default.
    expect(screen.getByTestId('manage-checkbox')).not.toBeChecked();

    await userEvent.click(screen.getByTestId('allow-button'));

    await waitFor(() => expect(mockApprove).toHaveBeenCalledTimes(1));
    expect(mockApprove).toHaveBeenCalledWith(
      expect.objectContaining({
        client_id: 'client-123',
        redirect_uri: 'https://agent.example.com/callback',
        state: 'st-abc',
        code_challenge: 'chal-xyz',
        code_challenge_method: 'S256',
        resource: 'https://engine.example.com',
        approved_scopes: ['provision'],
        consent_nonce: 'nonce-1',
        deny: false,
      }),
    );
    await waitFor(() =>
      expect(hrefValue).toBe('https://agent.example.com/callback?code=abc'),
    );
  });

  it('grants manage only when the checkbox is ticked', async () => {
    mockInfo.mockResolvedValue({
      client_name: 'Claude Code',
      scopes: ['provision', 'manage'],
      redirect_uri_valid: true,
      consent_nonce: 'nonce-1',
    });
    mockApprove.mockResolvedValue({ redirect_url: 'https://agent.example.com/callback?code=abc' });

    renderPage();
    await screen.findByTestId('consent-card');

    await userEvent.click(screen.getByTestId('manage-checkbox'));
    await userEvent.click(screen.getByTestId('allow-button'));

    await waitFor(() => expect(mockApprove).toHaveBeenCalledTimes(1));
    expect(mockApprove.mock.calls[0]![0].approved_scopes).toEqual(['provision', 'manage']);
  });

  it('Deny posts deny:true with no approved scopes and follows the redirect', async () => {
    mockInfo.mockResolvedValue({
      client_name: 'Claude Code',
      scopes: ['provision', 'manage'],
      redirect_uri_valid: true,
      consent_nonce: 'nonce-1',
    });
    mockApprove.mockResolvedValue({
      redirect_url: 'https://agent.example.com/callback?error=access_denied',
    });

    renderPage();
    await screen.findByTestId('consent-card');

    await userEvent.click(screen.getByTestId('deny-button'));

    await waitFor(() => expect(mockApprove).toHaveBeenCalledTimes(1));
    const arg = mockApprove.mock.calls[0]![0];
    expect(arg.deny).toBe(true);
    expect(arg.approved_scopes).toEqual([]);
    await waitFor(() =>
      expect(hrefValue).toBe('https://agent.example.com/callback?error=access_denied'),
    );
  });

  it('shows an error and no Allow button when the redirect_uri is invalid', async () => {
    mockInfo.mockResolvedValue({
      client_name: 'Evil App',
      scopes: ['provision'],
      redirect_uri_valid: false,
      consent_nonce: '',
    });

    renderPage();

    expect(await screen.findByTestId('consent-error')).toBeInTheDocument();
    expect(screen.queryByTestId('allow-button')).not.toBeInTheDocument();
  });

  it('shows an error and no Allow button when authorize-info fails', async () => {
    mockInfo.mockRejectedValue(new Error('invalid_client'));

    renderPage();

    expect(await screen.findByTestId('consent-error')).toHaveTextContent('invalid_client');
    expect(screen.queryByTestId('allow-button')).not.toBeInTheDocument();
  });

  it('errors when required params are missing without calling authorize-info', async () => {
    setLocation('client_id=&redirect_uri=');

    renderPage();

    expect(await screen.findByTestId('consent-error')).toBeInTheDocument();
    expect(mockInfo).not.toHaveBeenCalled();
  });
});
