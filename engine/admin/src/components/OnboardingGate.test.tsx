import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import OnboardingGate from './OnboardingGate';

// ── API mock ──────────────────────────────────────────────────────────────────

vi.mock('../api/client', () => ({
  api: {
    health: vi.fn(),
    listModels: vi.fn(),
  },
}));

import { api } from '../api/client';
// Loosely-typed handle so mockResolvedValue accepts fixture literals without
// re-declaring the full HealthResponse / Model shapes.
const mockApi = api as unknown as {
  health: ReturnType<typeof vi.fn>;
  listModels: ReturnType<typeof vi.fn>;
};

const healthWith = (platformDefault: boolean) => ({
  status: 'ok',
  version: '0.1.0',
  uptime: '1h',
  agents_count: 0,
  platform_default_model: platformDefault,
});

function renderGate() {
  return render(
    <MemoryRouter initialEntries={['/dashboard']}>
      <Routes>
        <Route path="/onboarding" element={<div>WIZARD</div>} />
        <Route
          path="*"
          element={
            <OnboardingGate>
              <div>PROTECTED</div>
            </OnboardingGate>
          }
        />
      </Routes>
    </MemoryRouter>,
  );
}

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('OnboardingGate — platform default model', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sessionStorage.clear();
  });

  it('opens the gate when the deployment provides a platform default model (zero tenant models)', async () => {
    mockApi.health.mockResolvedValue(healthWith(true));
    mockApi.listModels.mockResolvedValue([]);

    renderGate();

    await waitFor(() => expect(screen.getByText('PROTECTED')).toBeInTheDocument());
    expect(screen.queryByText('WIZARD')).not.toBeInTheDocument();
  });

  it('forces onboarding when there is neither a platform default nor any tenant model', async () => {
    mockApi.health.mockResolvedValue(healthWith(false));
    mockApi.listModels.mockResolvedValue([]);

    renderGate();

    await waitFor(() => expect(screen.getByText('WIZARD')).toBeInTheDocument());
    expect(screen.queryByText('PROTECTED')).not.toBeInTheDocument();
  });

  it('opens the gate when the tenant has models even without a platform default', async () => {
    mockApi.health.mockResolvedValue(healthWith(false));
    mockApi.listModels.mockResolvedValue([{ id: 'm1', name: 'gpt' }]);

    renderGate();

    await waitFor(() => expect(screen.getByText('PROTECTED')).toBeInTheDocument());
    expect(screen.queryByText('WIZARD')).not.toBeInTheDocument();
  });
});
