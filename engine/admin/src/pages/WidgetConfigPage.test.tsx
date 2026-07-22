import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import WidgetConfigPage from './WidgetConfigPage';
import type { Schema } from '../types';

vi.mock('../api/client', () => ({
  api: {
    listSchemas: vi.fn(),
  },
}));

import { api } from '../api/client';
const mockApi = vi.mocked(api);

const chatSchema: Schema = {
  id: 's-1',
  name: 'support',
  agents_count: 1,
  created_at: '2026-01-01T00:00:00Z',
  chat_enabled: true,
};

const HINT_PATTERN = /Powered by SyntheticBrew/;

describe('WidgetConfigPage attribution hint', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockApi.listSchemas.mockResolvedValue([chatSchema]);
  });

  afterEach(() => {
    vi.unstubAllEnvs();
  });

  it('shows the badge hint with an upgrade link to landing /billing in external mode', async () => {
    vi.stubEnv('VITE_AUTH_MODE', 'external');
    vi.stubEnv('VITE_LANDING_URL', 'https://land.test');

    render(<WidgetConfigPage />);

    expect(await screen.findByText(HINT_PATTERN)).toBeInTheDocument();
    const link = screen.getByRole('link', { name: 'upgrade' });
    expect(link).toHaveAttribute('href', 'https://land.test/billing');
    expect(link).toHaveAttribute('target', '_blank');
  });

  it('does not render the hint in local mode', async () => {
    render(<WidgetConfigPage />);

    // Wait for the snippet panel to render, then assert the hint is absent.
    expect(await screen.findByText('Embed Snippet')).toBeInTheDocument();
    expect(screen.queryByText(HINT_PATTERN)).not.toBeInTheDocument();
  });

  it('does not render the hint in external mode without a landing URL', async () => {
    vi.stubEnv('VITE_AUTH_MODE', 'external');

    render(<WidgetConfigPage />);

    expect(await screen.findByText('Embed Snippet')).toBeInTheDocument();
    expect(screen.queryByText(HINT_PATTERN)).not.toBeInTheDocument();
  });
});
