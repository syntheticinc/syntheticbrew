// ============================================================================
// Agent types
// ============================================================================

export interface AgentInfo {
  name: string;
  description?: string;
  tools_count: number;
  has_knowledge: boolean;
  is_system?: boolean;
  used_in_schemas?: string[];
}

export interface AgentDetail extends AgentInfo {
  model_id?: string;
  system_prompt: string;
  tools: string[];
  can_spawn: string[];
  lifecycle: 'persistent' | 'spawn';
  tool_execution: 'sequential' | 'parallel';
  max_steps: number;
  max_context_size: number;
  max_turn_duration: number;
  temperature?: number;
  top_p?: number;
  max_tokens?: number;
  stop_sequences?: string[];
  confirm_before: string[];
  mcp_servers: string[];
}

export interface CreateAgentRequest {
  name: string;
  model_id?: string;
  system_prompt: string;
  lifecycle?: string;
  tool_execution?: string;
  max_steps?: number;
  max_context_size?: number;
  max_turn_duration?: number;
  temperature?: number;
  top_p?: number;
  max_tokens?: number;
  stop_sequences?: string[];
  confirm_before?: string[];
  tools?: string[];
  can_spawn?: string[];
  mcp_servers?: string[];
}

// ============================================================================
// Model types
// ============================================================================

// ModelKind is the canonical split between chat-generating and embedding
// models (Wave 5). Backend enforces this at the API layer — agents' model_id
// must reference a `chat` model; KBs' embedding_model_id must reference an
// `embedding` model. Defaults to `chat` for new models created without an
// explicit kind.
export type ModelKind = 'chat' | 'embedding';

export interface Model {
  id: string;
  name: string;
  type: string;
  kind: ModelKind;
  base_url?: string;
  model_name: string;
  has_api_key: boolean;
  api_version?: string;
  embedding_dim?: number; // >0 for embedding models
  // is_default marks the single default model per (tenant, kind) — backend
  // enforces the "at most one default per kind" invariant via a partial
  // unique index. Optional on the read side for backward compat with any
  // stale response that predates the schema migration.
  is_default?: boolean;
  created_at: string;
}

export interface CreateModelRequest {
  name: string;
  type: string;
  kind?: ModelKind; // defaults to 'chat' server-side
  base_url?: string;
  model_name: string;
  api_key?: string;
  api_version?: string;
  embedding_dim?: number; // required when kind=embedding
  // Optional — when true, the server atomically clears the previous default
  // for this (tenant, kind) and sets this row as default in one transaction.
  // The onboarding wizard does not need to pass this; the backend
  // auto-promotes the first chat model per tenant on create.
  is_default?: boolean;
}

export interface UpdateModelRequest extends CreateModelRequest {}

// ============================================================================
// MCP types
// ============================================================================

// V2 Commit Group C (§5.5, §5.6):
// - `is_well_known` removed — catalog origin is not persisted on installs
//   (installs are independent copies in `mcp_servers`).
// - `status` is no longer served by the MCP server list endpoint; it is
//   populated by a live ping in the UI (or left undefined while pinging).
export interface MCPServer {
  id: string;
  name: string;
  type: 'stdio' | 'http' | 'sse' | 'streamable-http';
  command?: string;
  args?: string[];
  url?: string;
  env_vars?: Record<string, string>;
  forward_headers?: string[];
  auth_type?: string;
  auth_key_env?: string;
  auth_token_env?: string;
  auth_client_id?: string;
  status?: MCPServerStatus;
  catalog_refresh_interval_seconds?: number | null;
  agents: string[];
}

export interface MCPServerStatus {
  status: 'connected' | 'error' | 'connecting' | 'disconnected';
  status_message?: string;
  tools_count: number;
  connected_at?: string;
}

export type MCPCatalogCategory = 'search' | 'data' | 'communication' | 'dev-tools' | 'productivity' | 'payments' | 'generic';

export interface MCPCatalogEnvVar {
  name: string;
  description?: string;
  required: boolean;
  secret?: boolean;
}

export interface MCPCatalogTool {
  name: string;
  description: string;
}

export interface MCPCatalogPackage {
  type: 'stdio' | 'remote' | 'docker';
  transport?: string;
  command?: string;
  args?: string[];
  image?: string;
  url_template?: string;
  env_vars?: MCPCatalogEnvVar[];
}

export interface MCPCatalogEntry {
  name: string;
  display: string;
  description?: string;
  category?: MCPCatalogCategory;
  verified?: boolean;
  packages: MCPCatalogPackage[];
  provided_tools?: MCPCatalogTool[];
}

export interface MCPCatalogResponse {
  version: string;
  servers: MCPCatalogEntry[];
}

export interface CreateMCPServerRequest {
  name: string;
  type: string;
  command?: string;
  args?: string[];
  url?: string;
  env_vars?: Record<string, string>;
  forward_headers?: string[];
  auth_type?: string;
  auth_key_env?: string;
  auth_token_env?: string;
  auth_client_id?: string;
  // Optional periodic tools/list refresh interval in seconds (30..86400).
  // null = disabled. Persisted on mcp_servers.catalog_refresh_interval_seconds.
  catalog_refresh_interval_seconds?: number | null;
}

// ============================================================================
// Task types
// ============================================================================

export interface TaskResponse {
  id: string;
  title: string;
  agent_name: string;
  status: string;
  source: string;
  priority: number;
  parent_task_id?: string | null;
  created_at: string;
}

export interface TaskDetailResponse extends TaskResponse {
  description?: string;
  acceptance_criteria?: string[];
  blocked_by?: string[];
  assigned_agent_id?: string;
  mode: string;
  result?: string;
  error?: string;
  started_at?: string;
  approved_at?: string;
  completed_at?: string;
}

export interface PaginatedTaskResponse {
  data: TaskResponse[];
  total: number;
  page: number;
  per_page: number;
  total_pages: number;
}

export interface CreateTaskRequest {
  title: string;
  description?: string;
  agent_name: string;
  mode?: 'interactive' | 'background';
  priority?: number; // 0=normal, 1=high, 2=critical
  acceptance_criteria?: string[];
  blocked_by?: string[];
  parent_task_id?: string;
  require_approval?: boolean;
}

// ============================================================================
// Token types
// ============================================================================

export interface APIToken {
  id: string;
  name: string;
  scopes_mask: number;
  created_at: string;
  last_used_at?: string;
}

export interface CreateTokenRequest {
  name: string;
  scopes_mask: number;
}

export interface CreateTokenResponse {
  id: string;
  name: string;
  token: string;
}

// ============================================================================
// Circuit Breaker types
// ============================================================================

export interface CircuitBreakerState {
  name: string;
  state: 'closed' | 'open' | 'half_open';
  failure_count: number;
  last_failure?: string | null;
}

// ============================================================================
// Health types
// ============================================================================

export interface HealthResponse {
  status: string;
  version: string;
  uptime: string;
  agents_count: number;
  update_available?: string;
}

// ============================================================================
// Settings types
// ============================================================================

export interface Setting {
  key: string;
  value: string;
  updated_at: string;
}

// ============================================================================
// Audit types
// ============================================================================

export interface AuditEntry {
  id: string;
  timestamp: string;
  actor_type: string;
  actor_id: string;
  action: string;
  resource: string;
  details: string;
}

// ToolCallEntry is a single row in the Tool Call Log — one tool call, its
// arguments, its result, its status and duration. Shape matches the Go
// handler in internal/delivery/http/tool_call_log_handler.go.
export interface ToolCallEntry {
  id: string;
  session_id: string;
  agent_name: string;
  tool_name: string;
  input: string;
  output: string;
  status: 'completed' | 'failed' | string;
  duration_ms: number;
  user_id: string;
  created_at: string;
}

export interface PaginatedResponse<T> {
  data: T[];
  total: number;
  page: number;
  per_page: number;
  total_pages: number;
}

// ============================================================================
// Auth types
// ============================================================================

// Wave 1+7: the admin SPA no longer POSTs credentials. `local-session` is
// called (no body) by the bootstrap path in useAuth when VITE_AUTH_MODE=local.
// In external mode the token is delivered via URL hash instead.
export interface LocalSessionResponse {
  access_token: string;
  expires_at: string;
  token_type: string;
}

// ============================================================================
// Tool metadata types
// ============================================================================

export type SecurityZone = 'safe' | 'caution' | 'dangerous';

export interface ToolMetadata {
  name: string;
  description: string;
  security_zone: SecurityZone;
  risk_warning?: string;
  hint?: string;
  companion?: string;
}

// ============================================================================
// V2: Schema types
// ============================================================================

export interface Schema {
  id: string;
  name: string;
  description?: string;
  agents?: string[];
  agents_count: number;
  is_system?: boolean;
  entry_agent_name?: string;
  created_at: string;
  // Chat-enabled schemas accept POST /api/v1/schemas/{id}/chat requests.
  // Replaces the old per-schema trigger of type="chat".
  chat_enabled?: boolean;
  chat_last_fired_at?: string;
}

// ============================================================================
// V2: Schema template catalog (Commit Group L, §2.2)
// ============================================================================

export type SchemaTemplateCategory = 'support' | 'sales' | 'internal' | 'generic';

export interface SchemaTemplateCapability {
  type: string;
  config?: Record<string, unknown>;
}

export interface SchemaTemplateAgent {
  name: string;
  system_prompt: string;
  model?: string;
  capabilities?: SchemaTemplateCapability[];
}

export interface SchemaTemplateRelation {
  source: string;
  target: string;
}

export interface SchemaTemplateDefinition {
  entry_agent_name: string;
  agents: SchemaTemplateAgent[];
  relations: SchemaTemplateRelation[];
  // V2: schemas use chat_enabled boolean instead of a triggers table.
  chat_enabled?: boolean;
}

export interface SchemaTemplate {
  name: string;
  display: string;
  description: string;
  category: SchemaTemplateCategory;
  icon?: string;
  version: string;
  definition: SchemaTemplateDefinition;
}

export interface SchemaTemplateListResponse {
  version: string;
  templates: SchemaTemplate[];
}

export interface ForkTemplateResponse {
  schema_id: string;
  schema_name: string;
  agent_ids: Record<string, string>;
}

// ============================================================================
// V2: Capability types
// ============================================================================

export type CapabilityType =
  | 'memory'
  | 'knowledge'
  | 'knowledge_graphs';

export interface CapabilityConfig {
  id?: string;
  agent_name?: string;
  type: CapabilityType;
  enabled: boolean;
  config: Record<string, unknown>;
}

// ============================================================================
// V2: Capability CRUD types
// ============================================================================

export interface Capability {
  id: string;
  agent_name: string;
  type: string;
  config: Record<string, unknown>;
  enabled: boolean;
}

export interface CreateCapabilityRequest {
  type: string;
  config: Record<string, unknown>;
  enabled: boolean;
}

export interface UpdateCapabilityRequest {
  config?: Record<string, unknown>;
  enabled?: boolean;
}

export const CAPABILITY_META: Record<CapabilityType, { label: string; icon: string; description: string }> = {
  memory:            { label: 'Memory',           icon: 'brain',          description: 'Per-schema cross-session persistence' },
  knowledge:         { label: 'Knowledge',        icon: 'book-open',      description: 'RAG sources (PDF, DOCX, TXT, MD, CSV)' },
  knowledge_graphs:  { label: 'Knowledge Graphs', icon: 'graph',          description: 'Structured taxonomy lookups exposed as tools' },
};

// ============================================================================
// V2: Sessions
// ============================================================================

export type SessionStatus = 'running' | 'completed' | 'failed' | 'blocked' | 'timeout';

export interface SessionSummary {
  session_id: string;
  entry_agent: string;
  status: SessionStatus;
  duration_ms: number;
  total_tokens: number;
  created_at: string;
}

export interface PaginatedSessions {
  sessions: SessionSummary[];
  total: number;
  page: number;
  per_page: number;
}

// ============================================================================
// V2: Widget snippet generator types (client-side only, not API entities)
// ============================================================================
// V2 drops the server-side widgets table entirely — see
// docs/architecture/agent-first-runtime.md §4.3. A widget is a client,
// configured purely through the <script> tag's data-* attributes. The
// admin "Widgets" page is a pure snippet generator; these types describe
// the form state for that generator, not a DB entity.

export type WidgetPosition = 'bottom-right' | 'bottom-left';
export type WidgetSize = 'compact' | 'standard' | 'full';

export interface WidgetSnippetConfig {
  // Engine 1.1.0+: schemas are addressed by name (operator-facing handle)
  // in URLs and the widget's data-schema attribute. UUID is internal-only.
  schemaName: string;
  primaryColor: string;
  position: WidgetPosition;
  size: WidgetSize;
  welcomeMessage: string;
  placeholderText: string;
  title: string;
}

// ============================================================================
// V2: Usage / Quota types
// ============================================================================

export interface UsageMetric {
  name: string;
  label: string;
  used: number;
  limit: number;
  unit: string;
}

export interface UsageData {
  plan: string;
  billing_cycle_start: string;
  billing_cycle_end: string;
  metrics: UsageMetric[];
  stripe_portal_url?: string;
}

// ============================================================================
// Model Registry types
// ============================================================================

export interface ModelRegistryEntry {
  id: string;
  display_name: string;
  provider: string;
  tier: number; // 1 = Orchestrator, 2 = Sub-agent, 3 = Utility
  context_window: number;
  supports_tools: boolean;
  pricing_input: number;
  pricing_output: number;
  description: string;
  recommended_for: string[];
}

export interface RegistryProviderInfo {
  id: string;
  display_name: string;
  auth_type: string;
  website: string;
}

// ============================================================================
// V2: Webhook & Auth types
// ============================================================================

export type WebhookAuthType = 'none' | 'api_key' | 'forward_headers' | 'oauth2';

export interface WebhookConfig {
  url: string;
  auth_type: WebhookAuthType;
  token?: string;
  client_id?: string;
  client_secret?: string;
  timeout_ms?: number;
}

// ============================================================================
// V2: Knowledge Base types (many-to-many)
// ============================================================================

export interface KnowledgeBase {
  id: string;
  name: string;
  description?: string;
  embedding_model_id?: string;
  file_count: number;
  linked_agents: string[];
  created_at: string;
  updated_at: string;
}

export interface CreateKnowledgeBaseRequest {
  name: string;
  description?: string;
  embedding_model_id: string;
}

export type KnowledgeFileStatus = 'uploading' | 'indexing' | 'ready' | 'error';

export interface KnowledgeFile {
  id?: string;
  knowledge_base_id?: string;
  name: string;
  type: string;
  size: string;
  uploaded_at: string;
  status: KnowledgeFileStatus;
  error?: string;
  chunk_count?: number;
}

export interface KnowledgeStatus {
  agent_name: string;
  total_files: number;
  indexed_files: number;
  status: 'ready' | 'indexing' | 'empty';
}

// ============================================================================
// Knowledge Graphs (KG) types — bundles of structured entities with JSON
// schemas. Backend endpoints under /api/v1/knowledge-graphs are pending; the
// admin SPA renders these via prototype mocks until the engine ships them.
// ============================================================================

export interface KGBundle {
  bundle_name: string;
  version: string;
  manifest: {
    entity_types?: string[];
    counts?: Record<string, number>;
    schema_hashes?: Record<string, string>;
  };
  created_at: string;
  updated_at: string;
}

export interface KGEntitySchema {
  bundle_name: string;
  entity_type: string;
  schema_json: Record<string, unknown>;
  schema_hash: string;
  id_field: string;
  expose_tools: string[];
  tool_description?: string;
}

/**
 * KGSummaryFields lifts the `x-summary-fields` annotation out of the raw
 * schema JSON so views can render it without re-parsing. Engine 1.4.0
 * introduced this annotation; absent / empty preserves the 1.3.x
 * bare-ids tool response shape.
 *
 * Callers extract via: schema.schema_json["x-summary-fields"] as string[]
 * — wrapped here so the page-level components stay typed.
 */
export function kgSummaryFields(schema: KGEntitySchema): string[] {
  const raw = (schema.schema_json as Record<string, unknown>)['x-summary-fields'];
  if (!Array.isArray(raw)) {
    return [];
  }
  return raw.filter((v): v is string => typeof v === 'string');
}

export interface KGEntity {
  bundle_name: string;
  entity_type: string;
  entity_id: string;
  data: Record<string, unknown>;
  schema_hash: string;
}

export interface KGEntitiesListResponse {
  items: KGEntity[];
  total: number;
  limit: number;
  offset: number;
}

// ============================================================================
// Session message types (for chat history restore)
// ============================================================================

/** @deprecated Use EventResponse instead */
export interface MessageResponse {
  id: string;
  role: 'user' | 'assistant' | 'tool' | 'system';
  content: string;
  tool_name?: string;
  created_at: string;
}

// EventResponse represents a runtime event from the session timeline.
export interface EventResponse {
  id: string;
  event_type:
    | 'user_message'
    | 'assistant_message'
    | 'tool_call'
    | 'tool_result'
    | 'reasoning'
    | 'system'
    | 'interrupt_request'
    | 'interrupt_resume';
  agent_id?: string;
  call_id?: string;
  payload: Record<string, unknown>;
  created_at: string;
}

// HITL Interrupt Primitive — engine 1.2.0+

/** Per-question entry of a `form` interrupt schema. */
export interface InterruptQuestion {
  id: string;
  label: string;
  type: 'text' | 'select' | 'multiselect';
  options?: { label: string; value?: string }[];
  default?: string;
}

/** Schema body of a `structured_output` interrupt — mirrors domain.StructuredOutput. */
export interface InterruptSchema {
  output_type: 'summary_table' | 'form' | 'info';
  title?: string;
  description?: string;
  rows?: { label: string; value: string }[];
  actions?: { label: string; type: 'primary' | 'secondary'; value: string }[];
  questions?: InterruptQuestion[];
}

/** Wire payload of `interrupt_request` SSE / event_log content. */
export interface InterruptRequestPayload {
  interrupt_id: string;
  kind: 'structured_output';
  schema: InterruptSchema;
}

/** Single answer in a resume submission. */
export interface InterruptAnswer {
  question_id: string;
  value: string;
  label?: string;
}

/** Wire payload of `interrupt_resume` SSE / event_log content. */
export interface InterruptResumePayload {
  interrupt_id: string;
  kind: 'structured_output';
  payload: { answers: InterruptAnswer[] };
}
