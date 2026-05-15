import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { AuthContext, type AuthContextType } from '../hooks/useAuth';
import MCPPage from './MCPPage';

vi.mock('../api/client', () => ({
  api: {
    listMCPServers: vi.fn(),
    listCatalog: vi.fn(),
    listCircuitBreakers: vi.fn().mockResolvedValue([]),
    createMCPServer: vi.fn(),
    updateMCPServer: vi.fn(),
    deleteMCPServer: vi.fn(),
    refreshMCPServer: vi.fn(),
    resetCircuitBreaker: vi.fn(),
  },
}));

import { api } from '../api/client';
const mockApi = vi.mocked(api);

const auth: AuthContextType = {
  isAuthenticated: true,
  logout: vi.fn(),
};

function renderPage() {
  return render(
    <AuthContext.Provider value={auth}>
      <MemoryRouter>
        <MCPPage />
      </MemoryRouter>
    </AuthContext.Provider>,
  );
}

describe('MCPPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders MCP servers list', async () => {
    mockApi.listMCPServers.mockResolvedValue([
      {
        id: '1',
        name: 'playwright',
        type: 'stdio' as const,
        command: 'npx',
        args: ['@anthropic/playwright-mcp'],
        agents: ['e2e-test'],
        status: { status: 'connected' as const, tools_count: 12, connected_at: '2026-03-17T10:00:00Z' },
      },
    ]);
    mockApi.listCatalog.mockResolvedValue([]);

    renderPage();

    await waitFor(() => {
      expect(screen.getByText('playwright')).toBeInTheDocument();
      expect(screen.getByText('connected')).toBeInTheDocument();
      expect(screen.getByText('12')).toBeInTheDocument();
    });
  });

  it('shows empty state', async () => {
    mockApi.listMCPServers.mockResolvedValue([]);
    mockApi.listCatalog.mockResolvedValue([]);

    renderPage();

    await waitFor(() => {
      expect(screen.getByText('No MCP servers configured')).toBeInTheDocument();
    });
  });

  // Refresh button: clicking it must call api.refreshMCPServer with the
  // selected server's name. The button is rendered inside the DetailPanel
  // actions, so we open the panel by clicking the row first.
  it('Refresh button calls api.refreshMCPServer on success', async () => {
    mockApi.listMCPServers.mockResolvedValue([
      {
        id: '1', name: 'chirp-tools', type: 'http' as const, url: 'http://upstream/v1',
        agents: [],
      },
    ]);
    mockApi.listCatalog.mockResolvedValue([]);
    mockApi.refreshMCPServer.mockResolvedValue({ name: 'chirp-tools', tools_count: 5 });

    renderPage();

    await waitFor(() => expect(screen.getByText('chirp-tools')).toBeInTheDocument());

    fireEvent.click(screen.getByText('chirp-tools'));
    const refreshBtn = await screen.findByText('Refresh');
    fireEvent.click(refreshBtn);

    await waitFor(() => {
      expect(mockApi.refreshMCPServer).toHaveBeenCalledWith('chirp-tools');
    });
  });

  it('Refresh button still calls api.refreshMCPServer on error path', async () => {
    mockApi.listMCPServers.mockResolvedValue([
      {
        id: '1', name: 'chirp-tools', type: 'http' as const, url: 'http://upstream/v1',
        agents: [],
      },
    ]);
    mockApi.listCatalog.mockResolvedValue([]);
    mockApi.refreshMCPServer.mockRejectedValue(new Error('not registered'));

    renderPage();

    await waitFor(() => expect(screen.getByText('chirp-tools')).toBeInTheDocument());

    fireEvent.click(screen.getByText('chirp-tools'));
    const refreshBtn = await screen.findByText('Refresh');
    fireEvent.click(refreshBtn);

    await waitFor(() => {
      expect(mockApi.refreshMCPServer).toHaveBeenCalledWith('chirp-tools');
    });
  });
});
