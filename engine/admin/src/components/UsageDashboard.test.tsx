import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import UsageDashboard from './UsageDashboard';
import type { UsageStatusData } from '../types';

vi.mock('../api/client', () => ({
  api: {
    getUsageStatus: vi.fn(),
  },
}));

import { api } from '../api/client';
const mockApi = vi.mocked(api);

const usage: UsageStatusData = {
  active_users: { used: 12, limit: 200 },
  schemas: { used: 2, limit: 3 },
  knowledge_documents: { used: 25, limit: 100 },
  turns: { used: 5, limit: null },
};

describe('UsageDashboard', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockApi.getUsageStatus.mockResolvedValue(usage);
  });

  afterEach(() => {
    vi.unstubAllEnvs();
  });

  it('renders the four usage bars with used/limit values', async () => {
    render(<UsageDashboard />);

    await waitFor(() => {
      expect(screen.getByText('Monthly active users')).toBeInTheDocument();
    });
    expect(screen.getByText('Schemas')).toBeInTheDocument();
    expect(screen.getByText('Knowledge documents')).toBeInTheDocument();
    expect(screen.getByText('Turns')).toBeInTheDocument();

    expect(screen.getByText('12 / 200')).toBeInTheDocument();
    expect(screen.getByText('2 / 3')).toBeInTheDocument();
    expect(screen.getByText('25 / 100')).toBeInTheDocument();
    expect(screen.getByText('6%')).toBeInTheDocument();
    expect(screen.getByText('67%')).toBeInTheDocument();
    expect(screen.getByText('25%')).toBeInTheDocument();
  });

  it('renders "Unlimited" and a 0% bar for a null limit', async () => {
    render(<UsageDashboard />);

    await waitFor(() => {
      expect(screen.getByText('5 / Unlimited')).toBeInTheDocument();
    });
    expect(screen.getByText('0%')).toBeInTheDocument();
  });

  it('shows Manage Plan link in external mode pointing at landing /billing', async () => {
    vi.stubEnv('VITE_AUTH_MODE', 'external');
    vi.stubEnv('VITE_LANDING_URL', 'https://land.test');

    render(<UsageDashboard />);

    const link = await screen.findByRole('link', { name: 'Manage Plan' });
    expect(link).toHaveAttribute('href', 'https://land.test/billing');
    expect(link).toHaveAttribute('target', '_blank');
    expect(link).toHaveAttribute('rel', 'noreferrer');
  });

  it('hides Manage Plan in local mode', async () => {
    vi.stubEnv('VITE_AUTH_MODE', 'local');

    render(<UsageDashboard />);

    await waitFor(() => {
      expect(screen.getByText('Monthly active users')).toBeInTheDocument();
    });
    expect(screen.queryByText('Manage Plan')).not.toBeInTheDocument();
  });

  it('hides Manage Plan in external mode without a landing URL', async () => {
    vi.stubEnv('VITE_AUTH_MODE', 'external');

    render(<UsageDashboard />);

    await waitFor(() => {
      expect(screen.getByText('Monthly active users')).toBeInTheDocument();
    });
    expect(screen.queryByText('Manage Plan')).not.toBeInTheDocument();
  });

  it('shows unavailable state when the request fails', async () => {
    mockApi.getUsageStatus.mockRejectedValue(new Error('boom'));

    render(<UsageDashboard />);

    await waitFor(() => {
      expect(screen.getByText('Usage data unavailable')).toBeInTheDocument();
    });
  });
});
