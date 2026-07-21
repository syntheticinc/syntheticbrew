import type {
  AgentInfo,
  AgentDetail,
  CreateAgentRequest,
  Model,
  ModelKind,
  CreateModelRequest,
  MCPServer,
  MCPCatalogEntry,
  MCPCatalogResponse,
  CreateMCPServerRequest,
  TaskResponse,
  TaskDetailResponse,
  PaginatedTaskResponse,
  CreateTaskRequest,
  APIToken,
  CreateTokenRequest,
  CreateTokenResponse,
  HealthResponse,
  Setting,
  LocalSessionResponse,
  ToolMetadata,
  AuditEntry,
  ToolCallEntry,
  PaginatedResponse,
  ModelRegistryEntry,
  RegistryProviderInfo,
  Schema,
  SchemaTemplate,
  SchemaTemplateListResponse,
  SchemaTemplateCategory,
  ForkTemplateResponse,
  PaginatedSessions,
  SessionSummary,
  UsageStatusData,
  Capability,
  CreateCapabilityRequest,
  UpdateCapabilityRequest,
  KnowledgeBase,
  CreateKnowledgeBaseRequest,
  KnowledgeFile,
  KnowledgeStatus,
  KGBundle,
  KGEntitySchema,
  KGEntity,
  KGEntitiesListResponse,
  CircuitBreakerState,
  MessageResponse,
  EventResponse,
} from '../types';

const BASE_URL = '/api/v1';

// handleUnauthorized recovers from a 401 according to the active auth mode.
// The SPA has no /login route — local mode re-mints inline, external mode
// bounces to the landing IdP, and missing landing config is a build error.
let recovering = false;
function handleUnauthorized(): void {
  if (typeof window === 'undefined') return;

  const mode = import.meta.env.VITE_AUTH_MODE;
  const landing = import.meta.env.VITE_LANDING_URL as string | undefined;

  if (mode === 'external') {
    if (!landing) {
      throw new Error('VITE_AUTH_MODE=external requires VITE_LANDING_URL');
    }
    const returnTo = encodeURIComponent(window.location.href);
    window.location.href = `${landing}/login?return_to=${returnTo}&reason=session_expired`;
    return;
  }

  // local mode: re-mint via /api/v1/auth/local-session. The guard drops
  // duplicate calls when several in-flight requests all 401 at once.
  if (recovering) return;
  recovering = true;
  void import('../hooks/useAuth')
    .then(({ bootstrapAuth }) => bootstrapAuth())
    .catch((err) => console.error('auth recovery failed', err))
    .finally(() => { recovering = false; });
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

class APIClient {
  private token: string | null = null;

  constructor() {
    this.token = localStorage.getItem('jwt');
  }

  setToken(token: string) {
    this.token = token;
    localStorage.setItem('jwt', token);
  }

  clearToken() {
    this.token = null;
    localStorage.removeItem('jwt');
  }

  isAuthenticated(): boolean {
    return this.token !== null;
  }

  private async request<T>(method: string, path: string, body?: unknown): Promise<T> {
    const headers: Record<string, string> = { 'Content-Type': 'application/json' };
    if (this.token) {
      headers['Authorization'] = `Bearer ${this.token}`;
    }

    const res = await fetch(`${BASE_URL}${path}`, {
      method,
      headers,
      body: body ? JSON.stringify(body) : undefined,
    });

    if (res.status === 401 && path !== '/auth/local-session') {
      this.clearToken();
      handleUnauthorized();
      throw new Error('Unauthorized');
    }

    if (!res.ok) {
      const text = await res.text();
      let message = text;
      try {
        const json = JSON.parse(text) as { error?: string };
        if (json.error) message = json.error;
      } catch {
        // use raw text
      }
      throw new Error(message);
    }

    const contentType = res.headers.get('Content-Type') ?? '';
    if (contentType.includes('application/json')) {
      return (await res.json()) as T;
    }
    return (await res.text()) as unknown as T;
  }

  // ---- Auth ----
  // localSession mints a short-lived JWT for the single local admin user.
  // Wave 1+7: replaces the old username/password login flow entirely. The
  // endpoint takes no body and no auth — the engine only exposes it when
  // VITE_AUTH_MODE=local and the deployment is a single-user self-hosted
  // setup. In external/cloud mode this endpoint is not reachable and the
  // admin must receive its token via the URL hash fragment instead.
  localSession() {
    return this.request<LocalSessionResponse>('POST', '/auth/local-session');
  }

  // ---- Agents ----
  listAgents() {
    return this.request<AgentInfo[]>('GET', '/agents');
  }
  getAgent(name: string) {
    return this.request<AgentDetail>('GET', `/agents/${encodeURIComponent(name)}`);
  }
  createAgent(data: CreateAgentRequest) {
    return this.request<AgentDetail>('POST', '/agents', data);
  }
  updateAgent(name: string, data: Partial<CreateAgentRequest>) {
    return this.request<AgentDetail>('PATCH', `/agents/${encodeURIComponent(name)}`, data);
  }
  deleteAgent(name: string) {
    return this.request<void>('DELETE', `/agents/${encodeURIComponent(name)}`);
  }

  // ---- Models ----
  //
  // Wave 5: Models are split by `kind` — chat models (generate completions,
  // used by agents) and embedding models (vectorize text, used by KBs). The
  // backend accepts `?kind=chat` or `?kind=embedding` to filter; omitting the
  // param returns both. Callers that specifically need one side must pass the
  // kind explicitly — the agent model dropdown uses `kind=chat`, the KB
  // wizard uses `kind=embedding`.
  listModels(params?: { kind?: ModelKind }) {
    const query = params?.kind ? `?kind=${encodeURIComponent(params.kind)}` : '';
    return this.request<Model[]>('GET', `/models${query}`);
  }
  createModel(data: CreateModelRequest) {
    return this.request<Model>('POST', '/models', data);
  }
  updateModel(name: string, data: CreateModelRequest) {
    return this.request<Model>('PATCH', `/models/${encodeURIComponent(name)}`, data);
  }
  // setDefaultModel promotes a single model to default for its (tenant, kind)
  // pair. The backend atomically clears the previous default and flips the
  // target row in one transaction; the partial unique index on `models`
  // guarantees the invariant at the DB level.
  //
  // The `name` param is actually the model's DB identifier as used by all
  // other model endpoints (PATCH /models/:name). We keep the naming
  // consistent with updateModel/deleteModel to avoid a one-off id vs name
  // convention just for this action.
  setDefaultModel(name: string) {
    return this.request<Model>('PATCH', `/models/${encodeURIComponent(name)}`, { is_default: true });
  }
  deleteModel(name: string) {
    return this.request<void>('DELETE', `/models/${encodeURIComponent(name)}`);
  }

  // ---- MCP Servers ----
  listMCPServers() {
    return this.request<MCPServer[]>('GET', '/mcp-servers');
  }
  createMCPServer(data: CreateMCPServerRequest) {
    return this.request<MCPServer>('POST', '/mcp-servers', data);
  }
  updateMCPServer(name: string, data: CreateMCPServerRequest) {
    return this.request<MCPServer>('PATCH', `/mcp-servers/${encodeURIComponent(name)}`, data);
  }
  deleteMCPServer(name: string) {
    return this.request<void>('DELETE', `/mcp-servers/${encodeURIComponent(name)}`);
  }
  // refreshMCPServer triggers a lightweight tools/list re-fetch on the engine
  // without recreating the MCP transport. Surfaces as the "Refresh now"
  // button on MCPPage so operators can pick up downstream rename/add/remove
  // of tools without waiting for the optional TTL refresher.
  refreshMCPServer(name: string) {
    return this.request<{ name: string; tools_count: number }>(
      'POST', `/mcp-servers/${encodeURIComponent(name)}/refresh`,
    );
  }

  // ---- Tasks ----
  listTasks(params?: Record<string, string>) {
    const qs = params ? '?' + new URLSearchParams(params).toString() : '';
    return this.request<PaginatedTaskResponse>('GET', `/tasks${qs}`);
  }
  listTasksPaginated(params: Record<string, string>) {
    const qs = '?' + new URLSearchParams(params).toString();
    return this.request<PaginatedTaskResponse>('GET', `/tasks${qs}`);
  }
  getTask(id: string) {
    return this.request<TaskDetailResponse>('GET', `/tasks/${id}`);
  }
  createTask(data: CreateTaskRequest) {
    return this.request<{ task_id: string; status: string }>('POST', '/tasks', data);
  }
  listSubtasks(parentId: string) {
    return this.request<TaskResponse[]>('GET', `/tasks/${parentId}/subtasks`);
  }
  approveTask(id: string) {
    return this.request<void>('POST', `/tasks/${id}/approve`);
  }
  startTask(id: string) {
    return this.request<void>('POST', `/tasks/${id}/start`);
  }
  completeTask(id: string, result?: string) {
    return this.request<void>('POST', `/tasks/${id}/complete`, result ? { result } : undefined);
  }
  failTask(id: string, reason: string) {
    return this.request<void>('POST', `/tasks/${id}/fail`, { reason });
  }
  setTaskPriority(id: string, priority: number) {
    return this.request<void>('POST', `/tasks/${id}/priority`, { priority });
  }
  cancelTask(id: string) {
    return this.request<void>('DELETE', `/tasks/${id}`);
  }

  // ---- Health ----
  health() {
    return this.request<HealthResponse>('GET', '/health');
  }

  // ---- Tokens ----
  listTokens() {
    return this.request<APIToken[]>('GET', '/auth/tokens');
  }
  createToken(data: CreateTokenRequest) {
    return this.request<CreateTokenResponse>('POST', '/auth/tokens', data);
  }
  deleteToken(id: string) {
    return this.request<void>('DELETE', `/auth/tokens/${id}`);
  }

  // ---- Settings ----
  listSettings() {
    // API may return Setting[] or flat object depending on backend implementation
    return this.request<Setting[] | Record<string, unknown>>('GET', '/settings');
  }
  updateSetting(key: string, value: string) {
    return this.request<Setting>('PUT', `/settings/${encodeURIComponent(key)}`, { value });
  }

  // ---- Tools ----
  listToolMetadata() {
    return this.request<ToolMetadata[]>('GET', '/tools/metadata');
  }

  // ---- Config ----
  reloadConfig() {
    return this.request<{ reloaded: boolean; agents_count: number }>('POST', '/config/reload');
  }
  exportConfig() {
    return this.request<string>('GET', '/config/export');
  }
  importConfig(yamlContent: string) {
    return this.requestRaw<{ imported: boolean; agents_count: number }>('POST', '/config/import', yamlContent, 'text/yaml');
  }

  // ---- Audit ----
  listAuditLogs(params: Record<string, string> = {}) {
    const qs = Object.keys(params).length ? '?' + new URLSearchParams(params).toString() : '';
    return this.request<PaginatedResponse<AuditEntry>>('GET', `/audit${qs}`);
  }

  // ---- Tool Call Log (per-call observability — OSS Phase 4) ----
  // Filters: session_id, agent, tool, status (completed|failed), user_id,
  // from, to (RFC3339 or YYYY-MM-DD), page, per_page.
  listToolCalls(params: Record<string, string> = {}) {
    const qs = Object.keys(params).length ? '?' + new URLSearchParams(params).toString() : '';
    return this.request<PaginatedResponse<ToolCallEntry>>('GET', `/audit/tool-calls${qs}`);
  }

  // ---- Model Registry ----
  getModelRegistry(filters?: { provider?: string; tier?: number }) {
    const params = new URLSearchParams();
    if (filters?.provider) params.set('provider', filters.provider);
    if (filters?.tier) params.set('tier', String(filters.tier));
    const qs = params.toString() ? '?' + params.toString() : '';
    return this.request<ModelRegistryEntry[]>('GET', `/models/registry${qs}`);
  }

  getRegistryProviders() {
    return this.request<RegistryProviderInfo[]>('GET', `/models/registry/providers`);
  }

  // ─── Schemas ─────────────────────────────────────────────────────────────────

  listSchemas() {
    return this.request<Schema[]>('GET', '/schemas');
  }

  getSchema(schemaName: string) {
    return this.request<Schema>('GET', `/schemas/${encodeURIComponent(schemaName)}`);
  }

  createSchema(data: { name: string; description?: string }) {
    return this.request<Schema>('POST', '/schemas', data);
  }

  // updateSchema persists editable schema fields (description, chat_enabled,
  // entry_agent_id). Used by SchemaDetailPage's Settings tab to flip the
  // chat_enabled toggle — matches backend `PATCH /api/v1/schemas/{name}`.
  // Engine 1.1.0+: schema name is immutable; PATCH with a different `name`
  // returns 409 Conflict. The Name input in Settings is readOnly.
  updateSchema(schemaName: string, data: { name?: string; description?: string; chat_enabled?: boolean; entry_agent_id?: string }) {
    return this.request<Schema>('PATCH', `/schemas/${encodeURIComponent(schemaName)}`, data);
  }

  deleteSchema(schemaName: string) {
    return this.request<void>('DELETE', `/schemas/${encodeURIComponent(schemaName)}`);
  }

  // chatUrlForSchema returns the chat URL for a schema. Chat is SSE-streamed
  // from the backend (see useSSEChat hook), so this helper simply computes
  // the canonical endpoint — callers pass the URL to useSSEChat via the
  // `endpoint` config. Kept here for consistency with other API helpers.
  chatUrlForSchema(schemaName: string): string {
    return `${BASE_URL}/schemas/${encodeURIComponent(schemaName)}/chat`;
  }

  // V2: schema membership is derived from agent_relations
  // (docs/architecture/agent-first-runtime.md §2.1). The list endpoint is
  // read-only; mutation goes through the agent-relations endpoints below —
  // creating a relation adds both endpoints as implicit members; deleting
  // the last relation that referenced an agent removes it from the schema.
  listSchemaAgents(schemaName: string) {
    return this.request<string[]>('GET', `/schemas/${encodeURIComponent(schemaName)}/agents`);
  }

  // ─── Schema Templates (V2 Commit Group L, §2.2) ──────────────────────────
  //
  // Browse curated starter templates and fork one into a new tenant-owned
  // schema graph (schemas + agents + agent_relations + triggers). Forked
  // schemas are independent of the catalog — updating the YAML does not
  // modify existing forks.

  listSchemaTemplates(filter?: { category?: SchemaTemplateCategory; q?: string }) {
    const qs = new URLSearchParams();
    if (filter?.category) qs.set('category', filter.category);
    if (filter?.q) qs.set('q', filter.q);
    const suffix = qs.toString() ? `?${qs.toString()}` : '';
    return this.request<SchemaTemplateListResponse>('GET', `/schema-templates${suffix}`);
  }

  getSchemaTemplate(name: string) {
    return this.request<SchemaTemplate>('GET', `/schema-templates/${encodeURIComponent(name)}`);
  }

  forkSchemaTemplate(templateName: string, schemaName: string) {
    return this.request<ForkTemplateResponse>(
      'POST',
      `/schema-templates/${encodeURIComponent(templateName)}/fork`,
      { schema_name: schemaName },
    );
  }

  // ─── Agent Relations (V2 delegation) ──────────────────────────────────────
  //
  // V2 has a single implicit DELEGATION relationship type
  // (docs/architecture/agent-first-runtime.md §3.1). Adding an agent to a
  // schema is done by creating a relation from an existing schema member
  // (typically the entry agent / the parent in the delegation tree) to the
  // new agent.

  listAgentRelations(schemaName: string) {
    return this.request<{ id: string; schema_id: string; source: string; target: string }[]>(
      'GET', `/schemas/${encodeURIComponent(schemaName)}/agent-relations`,
    );
  }

  createAgentRelation(schemaName: string, source: string, target: string) {
    return this.request<{ id: string; schema_id: string; source: string; target: string }>(
      'POST', `/schemas/${encodeURIComponent(schemaName)}/agent-relations`, { source, target },
    );
  }

  deleteAgentRelation(schemaName: string, relationId: string) {
    return this.request<void>('DELETE', `/schemas/${encodeURIComponent(schemaName)}/agent-relations/${relationId}`);
  }

  // ─── Sessions / Inspect ──────────────────────────────────────────────────────

  async listSessions(params?: {
    page?: number;
    per_page?: number;
    search?: string;
    status?: string[];
    sort_by?: string;
    sort_dir?: 'asc' | 'desc';
    from?: string;
    to?: string;
    agent_name?: string;
  }): Promise<PaginatedSessions> {

    const qs = new URLSearchParams();
    if (params?.page) qs.set('page', String(params.page));
    if (params?.per_page) qs.set('per_page', String(params.per_page));
    if (params?.search) qs.set('search', params.search);
    if (params?.status) qs.set('status', params.status.join(','));
    if (params?.sort_by) qs.set('sort_by', params.sort_by);
    if (params?.sort_dir) qs.set('sort_dir', params.sort_dir);
    if (params?.from) qs.set('from', params.from);
    if (params?.to) qs.set('to', params.to);
    if (params?.agent_name) qs.set('agent_name', params.agent_name);
    const q = qs.toString() ? '?' + qs.toString() : '';
    // Backend returns { data: [...], total, page, per_page } with fields id/agent_name — map to SessionSummary
    type RawSession = { id?: string; session_id?: string; agent_name?: string; entry_agent?: string; status: string; duration_ms?: number; total_tokens?: number; created_at: string };
    const raw = await this.request<{ data?: RawSession[]; sessions?: RawSession[]; total: number; page: number; per_page: number }>('GET', `/sessions${q}`);
    const sessions: SessionSummary[] = (raw.data ?? raw.sessions ?? []).map((s) => ({
      session_id: s.session_id ?? s.id ?? '',
      entry_agent: s.entry_agent ?? s.agent_name ?? '',
      status: s.status as SessionSummary['status'],
      duration_ms: s.duration_ms ?? 0,
      total_tokens: s.total_tokens ?? 0,
      created_at: s.created_at,
    }));
    return { sessions, total: raw.total, page: raw.page, per_page: raw.per_page };
  }

  deleteSession(sessionId: string): Promise<void> {
    return this.request<void>('DELETE', `/sessions/${sessionId}`);
  }

  getSessionMessages(sessionId: string): Promise<MessageResponse[]> {
    return this.request<MessageResponse[]>('GET', `/sessions/${sessionId}/messages`);
  }

  getSessionEvents(sessionId: string): Promise<EventResponse[]> {
    return this.request<EventResponse[]>('GET', `/sessions/${sessionId}/messages`);
  }

  async getBuilderLastSession(): Promise<string | null> {
    try {
      const res = await this.request<{ session_id: string }>('GET', '/admin/assistant/last-session');
      return res.session_id ?? null;
    } catch {
      return null;
    }
  }

  // ─── Widgets ─────────────────────────────────────────────────────────────────
  // V2: widgets are not a domain entity — the admin page is a pure snippet
  // generator that emits a <script> tag client-side. No API calls needed.
  // See docs/architecture/agent-first-runtime.md §4.3.

  // ─── Usage / Quota ───────────────────────────────────────────────────────────

  getUsageStatus(): Promise<UsageStatusData> {
    return this.request<UsageStatusData>('GET', '/usage-status');
  }

  // ─── MCP Catalog ───────────────────────────────────────────────────────────────

  async listCatalog(category?: string, query?: string): Promise<MCPCatalogEntry[]> {
    const params = new URLSearchParams();
    if (category) params.set('category', category);
    if (query) params.set('q', query);
    const qs = params.toString() ? '?' + params.toString() : '';
    const resp = await this.request<MCPCatalogResponse>('GET', `/mcp/catalog${qs}`);
    return resp.servers ?? [];
  }

  // ─── Capabilities ──────────────────────────────────────────────────────────────

  async listCapabilities(agentName: string): Promise<Capability[]> {
    return this.request<Capability[]>('GET', `/agents/${encodeURIComponent(agentName)}/capabilities`);
  }

  async addCapability(agentName: string, data: CreateCapabilityRequest): Promise<Capability> {
    return this.request<Capability>('POST', `/agents/${encodeURIComponent(agentName)}/capabilities`, data);
  }

  async updateCapability(agentName: string, capId: string, data: UpdateCapabilityRequest): Promise<Capability> {
    return this.request<Capability>('PUT', `/agents/${encodeURIComponent(agentName)}/capabilities/${capId}`, data);
  }

  async removeCapability(agentName: string, capId: string): Promise<void> {
    return this.request<void>('DELETE', `/agents/${encodeURIComponent(agentName)}/capabilities/${capId}`);
  }

  // ─── Knowledge ──────────────────────────────────────────────────────────────

  async getKnowledgeStatus(agentName: string): Promise<KnowledgeStatus> {
    return this.request<KnowledgeStatus>('GET', `/agents/${encodeURIComponent(agentName)}/knowledge/status`);
  }

  async listKnowledgeFiles(agentName: string): Promise<KnowledgeFile[]> {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const raw = await this.request<any[]>('GET', `/agents/${encodeURIComponent(agentName)}/knowledge/files`);
    return (raw ?? []).map((r) => ({
      id: r.id,
      name: r.file_name ?? r.name ?? '',
      type: (r.file_type ?? r.type ?? '').toUpperCase(),
      size: r.file_size != null ? formatBytes(r.file_size) : (r.size ?? ''),
      uploaded_at: r.created_at ?? r.uploaded_at ?? '',
      status: r.status ?? 'ready',
      error: r.status_message,
      chunk_count: r.chunk_count,
    } as KnowledgeFile));
  }

  async deleteKnowledgeFile(agentName: string, fileId: string): Promise<void> {
    return this.request<void>('DELETE', `/agents/${encodeURIComponent(agentName)}/knowledge/files/${encodeURIComponent(fileId)}`);
  }

  async uploadKnowledgeFile(agentName: string, file: File): Promise<KnowledgeFile> {
    const formData = new FormData();
    formData.append('file', file);
    const headers: Record<string, string> = {};
    if (this.token) {
      headers['Authorization'] = `Bearer ${this.token}`;
    }
    const res = await fetch(`${BASE_URL}/agents/${encodeURIComponent(agentName)}/knowledge/files`, {
      method: 'POST',
      headers,
      body: formData,
    });
    if (res.status === 401) {
      this.clearToken();
      handleUnauthorized();
      throw new Error('Unauthorized');
    }
    if (!res.ok) {
      const text = await res.text();
      let message = text;
      try {
        const json = JSON.parse(text) as { error?: string };
        if (json.error) message = json.error;
      } catch { /* use raw text */ }
      throw new Error(message);
    }
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const r = (await res.json()) as any;
    return {
      id: r.id,
      name: r.file_name ?? r.name ?? '',
      type: (r.file_type ?? r.type ?? '').toUpperCase(),
      size: r.file_size != null ? formatBytes(r.file_size) : (r.size ?? ''),
      uploaded_at: r.created_at ?? r.uploaded_at ?? '',
      status: r.status ?? 'indexing',
      error: r.status_message,
      chunk_count: r.chunk_count,
    } as KnowledgeFile;
  }

  // ─── Knowledge Bases (many-to-many) ──────────────────────────────────────────

  async listKnowledgeBases(): Promise<KnowledgeBase[]> {
    return this.request<KnowledgeBase[]>('GET', '/knowledge-bases');
  }

  async getKnowledgeBase(name: string): Promise<KnowledgeBase> {
    return this.request<KnowledgeBase>('GET', `/knowledge-bases/${encodeURIComponent(name)}`);
  }

  async createKnowledgeBase(data: CreateKnowledgeBaseRequest): Promise<KnowledgeBase> {
    return this.request<KnowledgeBase>('POST', '/knowledge-bases', data);
  }

  // Engine 1.1.0+: KB name is immutable; PATCH with a different `name`
  // returns 409 Conflict. The Name input in the edit form is disabled.
  async updateKnowledgeBase(name: string, data: CreateKnowledgeBaseRequest): Promise<KnowledgeBase> {
    return this.request<KnowledgeBase>('PATCH', `/knowledge-bases/${encodeURIComponent(name)}`, data);
  }

  async deleteKnowledgeBase(name: string): Promise<void> {
    return this.request<void>('DELETE', `/knowledge-bases/${encodeURIComponent(name)}`);
  }

  async linkAgentToKB(kbName: string, agentName: string): Promise<void> {
    return this.request<void>('POST', `/knowledge-bases/${encodeURIComponent(kbName)}/agents/${encodeURIComponent(agentName)}`);
  }

  async unlinkAgentFromKB(kbName: string, agentName: string): Promise<void> {
    return this.request<void>('DELETE', `/knowledge-bases/${encodeURIComponent(kbName)}/agents/${encodeURIComponent(agentName)}`);
  }

  async listKBFiles(kbName: string): Promise<KnowledgeFile[]> {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const raw = await this.request<any[]>('GET', `/knowledge-bases/${encodeURIComponent(kbName)}/files`);
    return (raw ?? []).map((r) => ({
      id: r.id,
      knowledge_base_id: r.knowledge_base_id,
      name: r.file_name ?? r.name ?? '',
      type: (r.file_type ?? r.type ?? '').toUpperCase(),
      size: r.file_size != null ? formatBytes(r.file_size) : (r.size ?? ''),
      uploaded_at: r.created_at ?? r.uploaded_at ?? '',
      status: r.status ?? 'ready',
      error: r.status_message,
      chunk_count: r.chunk_count,
    } as KnowledgeFile));
  }

  async uploadKBFile(kbName: string, file: File): Promise<KnowledgeFile> {
    const formData = new FormData();
    formData.append('file', file);
    const headers: Record<string, string> = {};
    if (this.token) {
      headers['Authorization'] = `Bearer ${this.token}`;
    }
    const res = await fetch(`${BASE_URL}/knowledge-bases/${encodeURIComponent(kbName)}/files`, {
      method: 'POST',
      headers,
      body: formData,
    });
    if (res.status === 401) {
      this.clearToken();
      handleUnauthorized();
      throw new Error('Unauthorized');
    }
    if (!res.ok) {
      const text = await res.text();
      let message = text;
      try {
        const json = JSON.parse(text) as { error?: string };
        if (json.error) message = json.error;
      } catch { /* use raw text */ }
      throw new Error(message);
    }
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const r = (await res.json()) as any;
    return {
      id: r.id,
      name: r.file_name ?? r.name ?? '',
      type: (r.file_type ?? r.type ?? '').toUpperCase(),
      size: r.file_size != null ? formatBytes(r.file_size) : (r.size ?? ''),
      uploaded_at: r.created_at ?? r.uploaded_at ?? '',
      status: r.status ?? 'indexing',
      error: r.status_message,
      chunk_count: r.chunk_count,
    } as KnowledgeFile;
  }

  async deleteKBFile(kbName: string, fileId: string): Promise<void> {
    return this.request<void>('DELETE', `/knowledge-bases/${encodeURIComponent(kbName)}/files/${encodeURIComponent(fileId)}`);
  }

  // ─── Resilience ──────────────────────────────────────────────────────────────

  async listCircuitBreakers(): Promise<CircuitBreakerState[]> {
    const data = await this.request<{ breakers: CircuitBreakerState[] }>('GET', '/resilience/circuit-breakers');
    return data.breakers ?? [];
  }

  async resetCircuitBreaker(name: string): Promise<void> {
    await this.request<void>('POST', `/resilience/circuit-breakers/${encodeURIComponent(name)}/reset`);
  }

  // ─── Builder Assistant ───────────────────────────────────────────────────────

  async restoreBuilderAssistant(): Promise<void> {
    await this.request<void>('POST', '/admin/builder-assistant/restore', undefined);
  }

  // ─── Knowledge Graphs ────────────────────────────────────────────────────
  //
  // Backend endpoints under /api/v1/knowledge-graphs are not deployed yet.
  // Real calls surface whatever error the engine returns (typically a 404).

  async listKnowledgeGraphs(): Promise<KGBundle[]> {
    return this.request<KGBundle[]>('GET', '/knowledge-graphs');
  }

  async getKnowledgeGraph(bundleName: string): Promise<KGBundle> {
    return this.request<KGBundle>('GET', `/knowledge-graphs/${encodeURIComponent(bundleName)}`);
  }

  async listKGSchemas(bundleName: string): Promise<KGEntitySchema[]> {
    return this.request<KGEntitySchema[]>(
      'GET',
      `/knowledge-graphs/${encodeURIComponent(bundleName)}/schemas`,
    );
  }

  async getKGSchema(bundleName: string, entityType: string): Promise<KGEntitySchema> {
    return this.request<KGEntitySchema>(
      'GET',
      `/knowledge-graphs/${encodeURIComponent(bundleName)}/schemas/${encodeURIComponent(entityType)}`,
    );
  }

  async listKGEntities(
    bundleName: string,
    entityType: string,
    filters?: Record<string, string>,
    limit: number = 50,
    offset: number = 0,
  ): Promise<KGEntitiesListResponse> {
    const qp = new URLSearchParams();
    qp.set('limit', String(limit));
    qp.set('offset', String(offset));
    if (filters) {
      for (const [k, v] of Object.entries(filters)) {
        if (v) qp.set(`filter[${k}]`, v);
      }
    }
    return this.request<KGEntitiesListResponse>(
      'GET',
      `/knowledge-graphs/${encodeURIComponent(bundleName)}/entities/${encodeURIComponent(entityType)}?${qp.toString()}`,
    );
  }

  async getKGEntity(bundleName: string, entityType: string, entityID: string): Promise<KGEntity> {
    return this.request<KGEntity>(
      'GET',
      `/knowledge-graphs/${encodeURIComponent(bundleName)}/entities/${encodeURIComponent(entityType)}/${encodeURIComponent(entityID)}`,
    );
  }

  /**
   * Send a request with a raw (non-JSON) body.
   */
  private async requestRaw<T>(method: string, path: string, body: string, contentType: string): Promise<T> {
    const headers: Record<string, string> = { 'Content-Type': contentType };
    if (this.token) {
      headers['Authorization'] = `Bearer ${this.token}`;
    }

    const res = await fetch(`${BASE_URL}${path}`, {
      method,
      headers,
      body,
    });

    if (res.status === 401) {
      this.clearToken();
      handleUnauthorized();
      throw new Error('Unauthorized');
    }

    if (!res.ok) {
      const text = await res.text();
      let message = text;
      try {
        const json = JSON.parse(text) as { error?: string };
        if (json.error) message = json.error;
      } catch {
        // use raw text
      }
      throw new Error(message);
    }

    const ct = res.headers.get('Content-Type') ?? '';
    if (ct.includes('application/json')) {
      return (await res.json()) as T;
    }
    return (await res.text()) as unknown as T;
  }
}

export const api = new APIClient();
