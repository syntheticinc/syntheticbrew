import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import QuotaBanner from './QuotaBanner';
import type { UsageStatusData } from '../types';

vi.mock('../api/client', () => ({
  api: {
    getUsageStatus: vi.fn(),
  },
}));

import { api } from '../api/client';
const mockApi = vi.mocked(api);

function usageWith(turns: { used: number; limit: number | null }): UsageStatusData {
  return {
    active_users: { used: 1, limit: null },
    schemas: { used: 1, limit: null },
    knowledge_documents: { used: 1, limit: null },
    turns,
  };
}

describe('QuotaBanner', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sessionStorage.clear();
  });

  afterEach(() => {
    vi.unstubAllEnvs();
  });

  it('renders nothing below 80% usage', async () => {
    mockApi.getUsageStatus.mockResolvedValue(usageWith({ used: 10, limit: 50 }));

    const { container } = render(<QuotaBanner />);

    await waitFor(() => expect(mockApi.getUsageStatus).toHaveBeenCalled());
    expect(container.firstChild).toBeNull();
  });

  it('renders nothing when all limits are unconfigured (null)', async () => {
    mockApi.getUsageStatus.mockResolvedValue(usageWith({ used: 9999, limit: null }));

    const { container } = render(<QuotaBanner />);

    await waitFor(() => expect(mockApi.getUsageStatus).toHaveBeenCalled());
    expect(container.firstChild).toBeNull();
  });

  it('shows a warning banner at 80-99% with the worst metric label', async () => {
    mockApi.getUsageStatus.mockResolvedValue(usageWith({ used: 45, limit: 50 }));

    render(<QuotaBanner />);

    await waitFor(() => {
      expect(
        screen.getByText("You've used 90% of your Turns limit this month."),
      ).toBeInTheDocument();
    });
    // No upgrade link in local mode
    expect(screen.queryByRole('link', { name: 'Upgrade' })).not.toBeInTheDocument();
  });

  it('shows an Upgrade link to landing /billing in external mode', async () => {
    vi.stubEnv('VITE_AUTH_MODE', 'external');
    vi.stubEnv('VITE_LANDING_URL', 'https://land.test');
    mockApi.getUsageStatus.mockResolvedValue(usageWith({ used: 45, limit: 50 }));

    render(<QuotaBanner />);

    const link = await screen.findByRole('link', { name: 'Upgrade' });
    expect(link).toHaveAttribute('href', 'https://land.test/billing');
  });

  it('does NOT show the blocking modal at exactly 100% — soft indicator only', async () => {
    // A user consuming exactly their allotted quota is not over limit and
    // must not be greeted with a blocking "Limit Reached" modal.
    mockApi.getUsageStatus.mockResolvedValue(usageWith({ used: 50, limit: 50 }));

    render(<QuotaBanner />);

    await waitFor(() => {
      expect(screen.getByText(/Turns at 100%/)).toBeInTheDocument();
    });
    expect(screen.queryByText('Limit Reached')).not.toBeInTheDocument();
  });

  it('over limit (>100%) shows a NON-BLOCKING banner, never a blocking modal', async () => {
    // Regression: an over-limit tenant used to be walled behind a full-screen
    // "Limit Reached" modal whose Dismiss did not stick, so it could not reach
    // the delete action to self-recover. Over-limit must be a plain banner.
    mockApi.getUsageStatus.mockResolvedValue(usageWith({ used: 75, limit: 50 })); // 150%

    const { container } = render(<QuotaBanner />);

    await waitFor(() => {
      expect(screen.getByText(/Over your Turns limit \(150%\)/)).toBeInTheDocument();
    });
    // No blocking modal, and nothing renders a full-screen overlay.
    expect(screen.queryByText('Limit Reached')).not.toBeInTheDocument();
    expect(container.querySelector('.fixed.inset-0')).toBeNull();
  });

  it('over limit on a deletable resource surfaces the delete-to-recover hint', async () => {
    mockApi.getUsageStatus.mockResolvedValue({
      active_users: { used: 1, limit: null },
      schemas: { used: 3, limit: 2 }, // 150%
      knowledge_documents: { used: 1, limit: null },
      turns: { used: 1, limit: null },
    });

    render(<QuotaBanner />);

    await waitFor(() => {
      expect(
        screen.getByText(/Remove a schema to get back under your limit/),
      ).toBeInTheDocument();
    });
    expect(screen.queryByText('Limit Reached')).not.toBeInTheDocument();
  });
});
