import type {
  HealthResponse,
  Model,
  MCPServer,
  PaginatedTaskResponse,
  APIToken,
  Setting,
  AuditEntry,
  PaginatedResponse,
  MCPCatalogEntry,
} from '../types';

export const MOCK_HEALTH: HealthResponse = {
  status: 'ok',
  version: '2.0.0-prototype',
  uptime: '3h 42m',
  agents_count: 6,
};

export const MOCK_MODELS_LIST: Model[] = [
  // First chat model is marked default so the prototype UI renders the
  // "Default" badge — mirrors the backend behaviour where the first chat
  // model per tenant is auto-promoted on create.
  { id: '1', name: 'claude-haiku-3', type: 'openai_compatible', kind: 'chat', model_name: 'claude-3-haiku-20240307', has_api_key: true, is_default: true, created_at: '2026-03-01T00:00:00Z' },
  { id: '2', name: 'claude-sonnet-3.7', type: 'openai_compatible', kind: 'chat', model_name: 'claude-3-5-sonnet-20241022', has_api_key: true, is_default: false, created_at: '2026-03-01T00:00:00Z' },
  { id: '3', name: 'claude-opus-4', type: 'openai_compatible', kind: 'chat', model_name: 'claude-opus-4-20260414', has_api_key: true, is_default: false, created_at: '2026-03-15T00:00:00Z' },
  { id: '4', name: 'gpt-4o', type: 'openai_compatible', kind: 'chat', model_name: 'gpt-4o', has_api_key: true, is_default: false, base_url: 'https://api.openai.com/v1', created_at: '2026-03-10T00:00:00Z' },
  { id: '5', name: 'text-embed-3-small', type: 'embedding', kind: 'embedding', model_name: 'text-embedding-3-small', embedding_dim: 1536, has_api_key: true, base_url: 'https://api.openai.com/v1', created_at: '2026-03-12T00:00:00Z' },
];

export const MOCK_MCP_SERVERS: MCPServer[] = [
  { id: '1', name: 'google-sheets', type: 'stdio', command: 'npx', args: ['-y', '@anthropic/mcp-google-sheets'], status: { status: 'connected', tools_count: 12, connected_at: '2026-04-05T10:00:00Z' }, agents: ['support-agent'] },
  { id: '2', name: 'web-search', type: 'stdio', command: 'npx', args: ['-y', '@anthropic/mcp-web-search'], status: { status: 'connected', tools_count: 3, connected_at: '2026-04-05T10:00:00Z' }, agents: ['classifier', 'support-agent'] },
  { id: '3', name: 'slack-notifications', type: 'http', url: 'https://mcp.example.com/slack', status: { status: 'disconnected', status_message: 'Auth expired', tools_count: 5 }, agents: [] },
];

export const MOCK_CATALOG: MCPCatalogEntry[] = [
  {
    name: 'tavily-search',
    display: 'Tavily Web Search',
    description: 'AI-optimized web search with relevance scoring',
    category: 'search',
    verified: true,
    packages: [{
      type: 'stdio',
      command: 'npx',
      args: ['-y', 'tavily-mcp'],
      env_vars: [{ name: 'TAVILY_API_KEY', description: 'Tavily API key', required: true, secret: true }],
    }],
    provided_tools: [{ name: 'tavily_search', description: 'Search the web using Tavily' }],
  },
  {
    name: 'brave-search',
    display: 'Brave Search',
    description: 'Privacy-focused web search via Brave API',
    category: 'search',
    verified: true,
    packages: [{
      type: 'stdio',
      command: 'npx',
      args: ['-y', '@anthropic/brave-search-mcp'],
      env_vars: [{ name: 'BRAVE_API_KEY', description: 'Brave Search API key', required: true, secret: true }],
    }],
    provided_tools: [{ name: 'brave_search', description: 'Search the web using Brave' }],
  },
  {
    name: 'github',
    display: 'GitHub',
    description: 'GitHub API integration for repos, issues, PRs',
    category: 'dev-tools',
    verified: true,
    packages: [{
      type: 'stdio',
      command: 'npx',
      args: ['-y', '@anthropic/github-mcp'],
      env_vars: [{ name: 'GITHUB_TOKEN', description: 'GitHub personal access token', required: true, secret: true }],
    }],
    provided_tools: [
      { name: 'github_list_repos', description: 'List repositories' },
      { name: 'github_create_issue', description: 'Create an issue' },
    ],
  },
  {
    name: 'stripe',
    display: 'Stripe',
    description: 'Stripe payments API for charges, customers, subscriptions',
    category: 'payments',
    verified: true,
    packages: [{
      type: 'stdio',
      command: 'npx',
      args: ['-y', 'stripe-mcp'],
      env_vars: [{ name: 'STRIPE_API_KEY', description: 'Stripe secret key', required: true, secret: true }],
    }],
    provided_tools: [{ name: 'stripe_create_charge', description: 'Create a payment charge' }],
  },
  {
    name: 'remote-analytics',
    display: 'Analytics Dashboard',
    description: 'Remote analytics MCP server via streamable HTTP',
    category: 'data',
    verified: false,
    packages: [{
      type: 'remote',
      transport: 'streamable-http',
      url_template: 'https://analytics.example.com/mcp',
      env_vars: [{ name: 'ANALYTICS_TOKEN', description: 'API token', required: true, secret: true }],
    }],
    provided_tools: [{ name: 'query_metrics', description: 'Query analytics metrics' }],
  },
  {
    name: 'slack',
    display: 'Slack',
    description: 'Send messages and manage Slack channels',
    category: 'communication',
    verified: true,
    packages: [{
      type: 'stdio',
      command: 'npx',
      args: ['-y', 'slack-mcp'],
      env_vars: [{ name: 'SLACK_BOT_TOKEN', description: 'Slack bot OAuth token', required: true, secret: true }],
    }],
    provided_tools: [
      { name: 'slack_send_message', description: 'Send a message to a channel' },
      { name: 'slack_list_channels', description: 'List Slack channels' },
    ],
  },
];

export const MOCK_TASKS_PAGINATED: PaginatedTaskResponse = {
  data: [
    { id: '1', title: 'Process support ticket #4521', agent_name: 'support-agent', status: 'completed', source: 'webhook', priority: 0, created_at: '2026-04-05T14:30:00Z' },
    { id: '2', title: 'Analyze lead score batch', agent_name: 'lead-scorer', status: 'in_progress', source: 'cron', priority: 1, created_at: '2026-04-05T14:00:00Z' },
    { id: '3', title: 'Code review PR #89', agent_name: 'review-agent', status: 'failed', source: 'webhook', priority: 2, created_at: '2026-04-05T13:15:00Z' },
    { id: '4', title: 'Outreach to prospect', agent_name: 'outreach-agent', status: 'completed', source: 'cron', priority: 0, created_at: '2026-04-05T12:00:00Z' },
  ],
  total: 4,
  page: 1,
  per_page: 20,
  total_pages: 1,
};

export const MOCK_TOKENS: APIToken[] = [
  { id: '1', name: 'Production API', scopes_mask: 7, created_at: '2026-03-01T00:00:00Z', last_used_at: '2026-04-05T14:00:00Z' },
  { id: '2', name: 'CI/CD Pipeline', scopes_mask: 3, created_at: '2026-03-15T00:00:00Z', last_used_at: '2026-04-04T22:00:00Z' },
  { id: '3', name: 'Monitoring', scopes_mask: 1, created_at: '2026-04-01T00:00:00Z' },
];

export const MOCK_SETTINGS: Setting[] = [
  { key: 'default_model', value: 'claude-sonnet-3.7', updated_at: '2026-04-01T00:00:00Z' },
  { key: 'max_concurrent_sessions', value: '10', updated_at: '2026-03-20T00:00:00Z' },
  { key: 'session_timeout_minutes', value: '30', updated_at: '2026-03-20T00:00:00Z' },
  { key: 'enable_audit_log', value: 'true', updated_at: '2026-03-25T00:00:00Z' },
  { key: 'prototype_mode_enabled', value: 'true', updated_at: '2026-04-05T00:00:00Z' },
];

export const MOCK_AUDIT_LOGS: PaginatedResponse<AuditEntry> = {
  data: [
    { id: '1', timestamp: '2026-04-05T14:30:00Z', actor_type: 'user', actor_id: 'admin', action: 'agent.create', resource: 'support-agent', details: 'Created agent with model claude-sonnet-3.7' },
    { id: '2', timestamp: '2026-04-05T14:25:00Z', actor_type: 'user', actor_id: 'admin', action: 'model.create', resource: 'claude-opus-4', details: 'Added new model' },
    { id: '3', timestamp: '2026-04-05T14:20:00Z', actor_type: 'system', actor_id: 'engine', action: 'trigger.fired', resource: 'daily-report', details: 'Cron trigger executed' },
    { id: '4', timestamp: '2026-04-05T14:15:00Z', actor_type: 'agent', actor_id: 'support-agent', action: 'tool.called', resource: 'knowledge_search', details: 'Query: billing FAQ' },
    { id: '5', timestamp: '2026-04-05T14:10:00Z', actor_type: 'user', actor_id: 'admin', action: 'mcp.connect', resource: 'google-sheets', details: 'MCP server connected' },
  ],
  total: 5,
  page: 1,
  per_page: 20,
  total_pages: 1,
};

export const MOCK_CONFIG_YAML = `# SyntheticBrew Engine Configuration
server:
  host: 0.0.0.0
  port: 8443

database:
  url: postgres://syntheticbrew:password@localhost:5432/syntheticbrew

auth:
  admin_username: admin
  jwt_secret: "***"

agents:
  max_steps: 50
  max_context_size: 16000
  max_turn_duration: 120
  max_step_duration: 0
  default_model: claude-sonnet-3.7
`;
