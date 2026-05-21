import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { AuthContext, type AuthContextType } from '../hooks/useAuth';
import ModelsPage from './ModelsPage';

// jsdom doesn't support HTMLDialogElement.showModal/close
beforeEach(() => {
  HTMLDialogElement.prototype.showModal = vi.fn();
  HTMLDialogElement.prototype.close = vi.fn();
});

vi.mock('../api/client', () => ({
  api: {
    listModels: vi.fn(),
    createModel: vi.fn(),
    updateModel: vi.fn(),
    deleteModel: vi.fn(),
    getModelRegistry: vi.fn(),
    getRegistryProviders: vi.fn(),
  },
}));

import { api } from '../api/client';
const mockApi = vi.mocked(api);

const auth: AuthContextType = {
  isAuthenticated: true,
  logout: vi.fn(),
};

function renderModelsPage() {
  return render(
    <AuthContext.Provider value={auth}>
      <MemoryRouter>
        <ModelsPage />
      </MemoryRouter>
    </AuthContext.Provider>,
  );
}

const MOCK_REGISTRY = [
  {
    id: 'openai/gpt-4o',
    display_name: 'GPT-4o',
    provider: 'openrouter',
    tier: 1,
    context_window: 128000,
    supports_tools: true,
    pricing_input: 2.5,
    pricing_output: 10,
    description: 'OpenAI flagship model',
    recommended_for: ['orchestrator', 'coding'],
  },
  {
    id: 'anthropic/claude-3.5-sonnet',
    display_name: 'Claude 3.5 Sonnet',
    provider: 'openrouter',
    tier: 1,
    context_window: 200000,
    supports_tools: true,
    pricing_input: 3,
    pricing_output: 15,
    description: 'Anthropic flagship model',
    recommended_for: ['orchestrator', 'analysis'],
  },
  {
    id: 'qwen/qwen3-coder',
    display_name: 'Qwen3 Coder',
    provider: 'openrouter',
    tier: 2,
    context_window: 32000,
    supports_tools: true,
    pricing_input: 0.5,
    pricing_output: 1,
    description: 'Good for sub-agent tasks',
    recommended_for: ['sub-agent', 'coding'],
  },
];

const MOCK_MODELS = [
  {
    id: '1',
    name: 'main-model',
    type: 'openrouter',
    kind: 'chat' as const,
    base_url: 'https://openrouter.ai/api/v1',
    model_name: 'openai/gpt-4o',
    has_api_key: true,
    created_at: '2026-01-01T00:00:00Z',
  },
  {
    id: '2',
    name: 'custom-model',
    type: 'ollama',
    kind: 'chat' as const,
    base_url: 'http://localhost:11434',
    model_name: 'my-custom-llama',
    has_api_key: false,
    created_at: '2026-01-02T00:00:00Z',
  },
  {
    id: '3',
    name: 'embed-small',
    type: 'embedding',
    kind: 'embedding' as const,
    base_url: 'https://api.openai.com/v1',
    model_name: 'text-embedding-3-small',
    embedding_dim: 1536,
    has_api_key: true,
    created_at: '2026-01-03T00:00:00Z',
  },
];

describe('ModelsPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    // Reset the persisted kind filter so each test starts with "All".
    localStorage.removeItem('syntheticbrew_models_kind_filter');
    // Filter-aware mock: mirror the backend behavior so the kind-filter tests
    // can assert the table re-renders with the right slice.
    mockApi.listModels.mockImplementation((params?: { kind?: 'chat' | 'embedding' }) => {
      if (params?.kind === 'chat') return Promise.resolve(MOCK_MODELS.filter((m) => m.kind === 'chat'));
      if (params?.kind === 'embedding') return Promise.resolve(MOCK_MODELS.filter((m) => m.kind === 'embedding'));
      return Promise.resolve(MOCK_MODELS);
    });
    mockApi.getModelRegistry.mockResolvedValue(MOCK_REGISTRY);
  });

  it('renders models list with tier badges', async () => {
    renderModelsPage();

    await waitFor(() => {
      expect(screen.getByText('main-model')).toBeInTheDocument();
      expect(screen.getByText('custom-model')).toBeInTheDocument();
    });

    // Tier 1 badge for openai/gpt-4o
    expect(screen.getByText('Tier 1 - Orchestrator')).toBeInTheDocument();

    // Custom badge for the unknown model — scope the lookup to the
    // custom-model row so we don't collide with other "Custom" text
    // that the Wave 5 Kind column / filters may render elsewhere.
    const customRow = screen.getByText('custom-model').closest('tr');
    expect(customRow).not.toBeNull();
    expect(within(customRow as HTMLElement).getByText('Custom')).toBeInTheDocument();
  });

  it('shows empty state when no models', async () => {
    mockApi.listModels.mockResolvedValue([]);
    renderModelsPage();

    await waitFor(() => {
      expect(screen.getByText('No models configured')).toBeInTheDocument();
    });
  });

  it('handles registry API failure gracefully', async () => {
    mockApi.getModelRegistry.mockRejectedValue(new Error('Network error'));
    renderModelsPage();

    // Should still render models without badges
    await waitFor(() => {
      expect(screen.getByText('main-model')).toBeInTheDocument();
      expect(screen.getByText('custom-model')).toBeInTheDocument();
    });
  });

  it('shows detail panel with tier info on row click', async () => {
    renderModelsPage();
    const user = userEvent.setup();

    await waitFor(() => {
      expect(screen.getByText('main-model')).toBeInTheDocument();
    });

    await user.click(screen.getByText('main-model'));

    await waitFor(() => {
      // Detail panel should show tier badge
      const tierBadges = screen.getAllByText('Tier 1 - Orchestrator');
      // One in table, one in detail panel
      expect(tierBadges.length).toBeGreaterThanOrEqual(2);
    });
  });

  it('opens form modal with provider options including openrouter', async () => {
    renderModelsPage();
    const user = userEvent.setup();

    await waitFor(() => {
      expect(screen.getByText('Add Model')).toBeInTheDocument();
    });

    await user.click(screen.getByText('Add Model'));

    await waitFor(() => {
      expect(screen.getByText('OpenRouter')).toBeInTheDocument();
      expect(screen.getByText('Azure OpenAI')).toBeInTheDocument();
      expect(screen.getByText('Google (Gemini)')).toBeInTheDocument();
    });
  });

  // Inline Display Name validation — mirrors the DNS-label slug rules
  // enforced by the backend (`name_validation.go`). Each invalid shape
  // gets its own dedicated error message so the user knows precisely
  // what to fix; the placeholder + hint show the rule up front.
  describe('Display Name validation', () => {
    async function openCreateForm() {
      renderModelsPage();
      const user = userEvent.setup();
      await waitFor(() => expect(screen.getByText('Add Model')).toBeInTheDocument());
      await user.click(screen.getByText('Add Model'));
      await waitFor(() => expect(screen.getByText('OpenRouter')).toBeInTheDocument());
      // Address Display Name by its DOM id (FormField derives `ff-display-name`).
      const input = document.getElementById('ff-display-name') as HTMLInputElement;
      expect(input).not.toBeNull();
      return { user, input };
    }

    it('shows the slug rule hint when the field is empty', async () => {
      await openCreateForm();
      expect(
        screen.getByText(/URL slug: lowercase letters, digits, hyphens/i),
      ).toBeInTheDocument();
    });

    it('rejects uppercase letters with a dedicated message', async () => {
      const { user, input } = await openCreateForm();
      await user.type(input, 'My-Llama');
      expect(
        screen.getByText(/Uppercase letters are not allowed/i),
      ).toBeInTheDocument();
    });

    it('rejects spaces with a dedicated message', async () => {
      const { user, input } = await openCreateForm();
      await user.type(input, 'my llama');
      expect(
        screen.getByText(/Spaces are not allowed — use hyphens instead/i),
      ).toBeInTheDocument();
    });

    it('rejects other forbidden characters', async () => {
      const { user, input } = await openCreateForm();
      await user.type(input, 'my_llama');
      expect(
        screen.getByText(/Only lowercase letters, digits, and hyphens are allowed/i),
      ).toBeInTheDocument();
    });

    it('rejects leading/trailing hyphens', async () => {
      const { user, input } = await openCreateForm();
      await user.type(input, '-bad');
      expect(
        screen.getByText(/Must start and end with a letter or digit/i),
      ).toBeInTheDocument();
    });

    it('accepts a valid slug — no error rendered', async () => {
      const { user, input } = await openCreateForm();
      await user.type(input, 'my-llama');
      // Error text variants from validateModelDisplayName must NOT appear.
      expect(screen.queryByText(/Uppercase letters are not allowed/i)).not.toBeInTheDocument();
      expect(screen.queryByText(/Spaces are not allowed/i)).not.toBeInTheDocument();
      expect(screen.queryByText(/Only lowercase letters, digits, and hyphens are allowed/i)).not.toBeInTheDocument();
    });

    it('blocks submit on invalid name and never calls createModel', async () => {
      const { user, input } = await openCreateForm();
      await user.type(input, 'BAD');
      await user.type(document.getElementById('ff-model-name') as HTMLInputElement, 'gpt-4o');
      // The Add Model button inside the modal triggers form submit.
      const buttons = screen.getAllByRole('button', { name: /^Add Model$/i });
      // The modal footer button is the last one — header button opens the modal.
      const submitBtn = buttons[buttons.length - 1];
      if (!submitBtn) throw new Error('Submit button not found');
      await user.click(submitBtn);
      expect(mockApi.createModel).not.toHaveBeenCalled();
    });
  });
});
