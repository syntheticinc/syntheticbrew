// Mock data for the delegation-tree UI prototype.
//
// This module is only consumed in prototype mode (isPrototype === true) —
// the production admin reads equivalent shapes from the Engine REST API.
// The types here match the wire-level shapes the canvas / overview / schema
// detail components expect after adaptation.

export interface TreeAgent {
  id: string;
  name: string;
  model: string;
  description?: string;
  avatarInitials: string; // fallback avatar: 2 letters
  lifecycle: 'persistent' | 'spawn';
  toolsCount: number;
  knowledgeCount: number;
  flowsCount: number;
  activeSessions: number;
  state: 'idle' | 'active' | 'degraded';
}

export interface TreeRelation {
  id: string;
  sourceAgentId: string;
  targetAgentId: string;
  config?: Record<string, unknown>;
}

export interface MockSchema {
  id: string;
  name: string;
  description: string;
  entryAgentId: string;
  agentIds: string[];
  triggerIds: string[];
  sessionsToday: number;
  activeSessions: number;
  lastActivityAt: string;
  updatedAt: string;
}

export interface MockSessionMessage {
  step: number;
  agentId: string;
  kind: 'user_message' | 'assistant_message' | 'tool_call' | 'tool_result' | 'reasoning' | 'delegation' | 'delegation_return';
  content: string;
  toolName?: string;
  toolArgs?: string;
  toolResult?: string;
  targetAgentId?: string;
  sourceAgentId?: string;
  timestamp: string;
}

export interface MockSession {
  id: string;
  schemaId: string;
  triggerId: string;
  title: string;
  status: 'active' | 'completed' | 'failed';
  startedAt: string;
  participantAgentIds: string[];
  messages: MockSessionMessage[];
}

export interface OverviewEvent {
  timestamp: string;
  kind: 'trigger_fired' | 'delegation' | 'session_completed' | 'agent_error' | 'flow_entered';
  summary: string;
  schemaId?: string;
  sessionId?: string;
}

// ============================================================================
// AGENTS (cross-schema library)
// ============================================================================

export const mockAgents: TreeAgent[] = [
  {
    id: 'agent-triage',
    name: 'Triage Orchestrator',
    model: 'claude-haiku-4-5',
    description: 'Classifies incoming requests and delegates to specialists.',
    avatarInitials: 'TR',
    lifecycle: 'persistent',
    toolsCount: 3,
    knowledgeCount: 1,
    flowsCount: 0,
    activeSessions: 3,
    state: 'active',
  },
  {
    id: 'agent-sales',
    name: 'Sales Specialist',
    model: 'claude-sonnet-4-6',
    description: 'Handles pricing, plans, conversion.',
    avatarInitials: 'SL',
    lifecycle: 'persistent',
    toolsCount: 6,
    knowledgeCount: 2,
    flowsCount: 1,
    activeSessions: 1,
    state: 'active',
  },
  {
    id: 'agent-tech',
    name: 'Tech Support',
    model: 'claude-sonnet-4-6',
    description: 'Debugging and technical assistance.',
    avatarInitials: 'TS',
    lifecycle: 'persistent',
    toolsCount: 8,
    knowledgeCount: 3,
    flowsCount: 1,
    activeSessions: 0,
    state: 'idle',
  },
  {
    id: 'agent-billing',
    name: 'Billing Agent',
    model: 'claude-haiku-4-5',
    description: 'Invoices, refunds, payment issues.',
    avatarInitials: 'BL',
    lifecycle: 'persistent',
    toolsCount: 4,
    knowledgeCount: 1,
    flowsCount: 0,
    activeSessions: 0,
    state: 'idle',
  },
  {
    id: 'agent-faq',
    name: 'FAQ Lookup',
    model: 'claude-haiku-4-5',
    description: 'Fast FAQ retrieval from knowledge base.',
    avatarInitials: 'FQ',
    lifecycle: 'spawn',
    toolsCount: 2,
    knowledgeCount: 4,
    flowsCount: 0,
    activeSessions: 0,
    state: 'idle',
  },
  {
    id: 'agent-escalation',
    name: 'Human Escalation',
    model: 'claude-sonnet-4-6',
    description: 'Routes to human operators via webhook.',
    avatarInitials: 'HE',
    lifecycle: 'spawn',
    toolsCount: 1,
    knowledgeCount: 0,
    flowsCount: 0,
    activeSessions: 0,
    state: 'idle',
  },
  {
    id: 'agent-sales-orch',
    name: 'Sales Qualification Orch',
    model: 'claude-opus-4-6',
    description: 'Qualifies leads via deep interview flow.',
    avatarInitials: 'SQ',
    lifecycle: 'persistent',
    toolsCount: 4,
    knowledgeCount: 1,
    flowsCount: 1,
    activeSessions: 1,
    state: 'active',
  },
  {
    id: 'agent-lead-researcher',
    name: 'Lead Researcher',
    model: 'claude-sonnet-4-6',
    description: 'Gathers public info about prospect.',
    avatarInitials: 'LR',
    lifecycle: 'persistent',
    toolsCount: 5,
    knowledgeCount: 0,
    flowsCount: 0,
    activeSessions: 0,
    state: 'idle',
  },
  {
    id: 'agent-closer',
    name: 'Closer',
    model: 'claude-opus-4-6',
    description: 'Final pitch and handoff to human AE.',
    avatarInitials: 'CL',
    lifecycle: 'persistent',
    toolsCount: 3,
    knowledgeCount: 1,
    flowsCount: 0,
    activeSessions: 0,
    state: 'idle',
  },
  {
    id: 'agent-health',
    name: 'Health Monitor',
    model: 'claude-haiku-4-5',
    description: 'Hourly system checks and alerting.',
    avatarInitials: 'HM',
    lifecycle: 'persistent',
    toolsCount: 5,
    knowledgeCount: 0,
    flowsCount: 0,
    activeSessions: 0,
    state: 'idle',
  },
  {
    id: 'agent-alerter',
    name: 'Alerter',
    model: 'claude-haiku-4-5',
    description: 'Dispatches alerts to Slack/PagerDuty.',
    avatarInitials: 'AL',
    lifecycle: 'spawn',
    toolsCount: 2,
    knowledgeCount: 0,
    flowsCount: 0,
    activeSessions: 0,
    state: 'idle',
  },
];

// ============================================================================
// RELATIONS (only delegation, source → target)
// ============================================================================

export const mockAgentRelations: TreeRelation[] = [
  // Support Schema
  { id: 'rel-1', sourceAgentId: 'agent-triage', targetAgentId: 'agent-sales' },
  { id: 'rel-2', sourceAgentId: 'agent-triage', targetAgentId: 'agent-tech' },
  { id: 'rel-3', sourceAgentId: 'agent-triage', targetAgentId: 'agent-billing' },
  { id: 'rel-4', sourceAgentId: 'agent-sales', targetAgentId: 'agent-faq' },
  { id: 'rel-5', sourceAgentId: 'agent-billing', targetAgentId: 'agent-escalation' },
  // Sales Schema
  { id: 'rel-6', sourceAgentId: 'agent-sales-orch', targetAgentId: 'agent-lead-researcher' },
  { id: 'rel-7', sourceAgentId: 'agent-sales-orch', targetAgentId: 'agent-closer' },
  // Health Schema
  { id: 'rel-8', sourceAgentId: 'agent-health', targetAgentId: 'agent-alerter' },
];

// ============================================================================
// SCHEMAS
// ============================================================================

// Engine 1.1.0+: schema names are kebab-case operator-facing handles. URL
// params and lookups go through `name`; `id` (UUID) is internal-only and
// kept here only for shapes that still surface it (sessions table, etc).
export const mockSchemas: MockSchema[] = [
  {
    id: 'mock-schema-customer-support',
    name: 'customer-support',
    description: 'Multi-channel customer support with triage and specialist delegation.',
    entryAgentId: 'agent-triage',
    agentIds: ['agent-triage', 'agent-sales', 'agent-tech', 'agent-billing', 'agent-faq', 'agent-escalation'],
    triggerIds: ['trg-support-chat-main', 'trg-support-webhook'],
    sessionsToday: 142,
    activeSessions: 3,
    lastActivityAt: '2026-04-15T12:35:00Z',
    updatedAt: '2026-04-14T09:15:00Z',
  },
  {
    id: 'mock-schema-sales-qualification',
    name: 'sales-qualification',
    description: 'Lead qualification flow with research and closing stages.',
    entryAgentId: 'agent-sales-orch',
    agentIds: ['agent-sales-orch', 'agent-lead-researcher', 'agent-closer'],
    triggerIds: ['trg-sales-webhook'],
    sessionsToday: 28,
    activeSessions: 1,
    lastActivityAt: '2026-04-15T12:30:00Z',
    updatedAt: '2026-04-13T14:00:00Z',
  },
  {
    id: 'mock-schema-daily-health',
    name: 'daily-health',
    description: 'Hourly system health checks with automated alerting.',
    entryAgentId: 'agent-health',
    agentIds: ['agent-health', 'agent-alerter'],
    triggerIds: ['trg-health-cron'],
    sessionsToday: 24,
    activeSessions: 0,
    lastActivityAt: '2026-04-15T12:00:00Z',
    updatedAt: '2026-04-10T08:30:00Z',
  },
];

// ============================================================================
// SESSIONS (for overview live panel in prototype mode)
// ============================================================================

// schemaId here references mockSchemas[].id (UUID-shaped mock prefix). The
// overview events panel uses these for cross-references; selectors that
// surface the canonical operator handle look up by name via getSchemaById.
export const mockSessions: MockSession[] = [
  {
    id: 'sess-a7f2',
    schemaId: 'mock-schema-customer-support',
    triggerId: 'trg-support-chat-main',
    title: 'Customer asking about enterprise pricing',
    status: 'active',
    startedAt: '2026-04-15T12:34:05Z',
    participantAgentIds: ['agent-triage', 'agent-sales', 'agent-faq'],
    messages: [],
  },
  {
    id: 'sess-b3d1',
    schemaId: 'mock-schema-customer-support',
    triggerId: 'trg-support-chat-main',
    title: 'Billing dispute — refund request',
    status: 'active',
    startedAt: '2026-04-15T12:33:40Z',
    participantAgentIds: ['agent-triage', 'agent-billing'],
    messages: [],
  },
  {
    id: 'sess-c9e4',
    schemaId: 'mock-schema-sales-qualification',
    triggerId: 'trg-sales-webhook',
    title: 'Lead qualification: Acme Corp',
    status: 'active',
    startedAt: '2026-04-15T12:30:00Z',
    participantAgentIds: ['agent-sales-orch', 'agent-lead-researcher'],
    messages: [],
  },
];

// ============================================================================
// OVERVIEW — recent-events feed for prototype mode
// ============================================================================

export const mockOverviewEvents: OverviewEvent[] = [
  {
    timestamp: '2026-04-15T12:34:22Z',
    kind: 'session_completed',
    summary: 'Triage finalized response for enterprise pricing inquiry.',
    schemaId: 'mock-schema-customer-support',
    sessionId: 'sess-a7f2',
  },
  {
    timestamp: '2026-04-15T12:34:19Z',
    kind: 'delegation',
    summary: 'FAQ returned result to Sales Specialist.',
    schemaId: 'mock-schema-customer-support',
    sessionId: 'sess-a7f2',
  },
  {
    timestamp: '2026-04-15T12:34:10Z',
    kind: 'delegation',
    summary: 'Triage delegated to Sales Specialist (SSO pricing inquiry).',
    schemaId: 'mock-schema-customer-support',
    sessionId: 'sess-a7f2',
  },
  {
    timestamp: '2026-04-15T12:34:05Z',
    kind: 'trigger_fired',
    summary: 'Support Widget trigger fired → Triage.',
    schemaId: 'mock-schema-customer-support',
  },
  {
    timestamp: '2026-04-15T12:33:47Z',
    kind: 'delegation',
    summary: 'Billing Agent received duplicate-charge lookup result.',
    schemaId: 'mock-schema-customer-support',
    sessionId: 'sess-b3d1',
  },
  {
    timestamp: '2026-04-15T12:33:40Z',
    kind: 'trigger_fired',
    summary: 'Chat Endpoint fired → Triage (billing dispute).',
    schemaId: 'mock-schema-customer-support',
  },
  {
    timestamp: '2026-04-15T12:30:02Z',
    kind: 'delegation',
    summary: 'Sales Qualification Orch delegated to Lead Researcher.',
    schemaId: 'mock-schema-sales-qualification',
    sessionId: 'sess-c9e4',
  },
  {
    timestamp: '2026-04-15T12:30:00Z',
    kind: 'flow_entered',
    summary: 'Sales Qualification Orch entered "Deep Qualification Interview".',
    schemaId: 'mock-schema-sales-qualification',
    sessionId: 'sess-c9e4',
  },
  {
    timestamp: '2026-04-15T12:00:03Z',
    kind: 'session_completed',
    summary: 'Health Monitor completed hourly check (all green).',
    schemaId: 'mock-schema-daily-health',
  },
  {
    timestamp: '2026-04-15T12:00:00Z',
    kind: 'trigger_fired',
    summary: 'Hourly cron fired → Health Monitor.',
    schemaId: 'mock-schema-daily-health',
  },
];

// ============================================================================
// Selectors
// ============================================================================

export function getAgentById(id: string): TreeAgent | undefined {
  return mockAgents.find((a) => a.id === id);
}

export function getSchemaById(id: string): MockSchema | undefined {
  return mockSchemas.find((s) => s.id === id);
}
