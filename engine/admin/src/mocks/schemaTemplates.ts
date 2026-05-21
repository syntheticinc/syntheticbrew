// V2 Schema Template catalog — prototype-mode mock.
//
// Mirrors the wire shape of `GET /api/v1/schema-templates` (see
// syntheticinc/engine/schema-templates.yaml + the SchemaTemplateListResponse
// Go DTO). Stays in sync with the YAML manually — the prototype-mode
// admin UI reads this file directly, production mode reads from DB.
//
// Commit Group L (§2.2).

import type { SchemaTemplate } from '../types';

export const MOCK_SCHEMA_TEMPLATES: SchemaTemplate[] = [
  {
    name: 'customer-support-basic',
    display: 'Customer Support (Basic)',
    description:
      "Triage orchestrator that delegates resolution to a knowledge-backed resolver. Start here for FAQ-style help desks.",
    category: 'support',
    icon: 'headset',
    version: '1.0',
    definition: {
      entry_agent_name: 'triage',
      agents: [
        {
          name: 'triage',
          system_prompt:
            'You are the triage orchestrator. Classify incoming customer requests and delegate the response to the resolver.',
          capabilities: [{ type: 'memory', config: {} }],
        },
        {
          name: 'resolver',
          system_prompt:
            'You resolve customer questions using the knowledge base. Be concise, accurate, and cite the source when helpful.',
          capabilities: [{ type: 'knowledge', config: {} }],
        },
      ],
      relations: [{ source: 'triage', target: 'resolver' }],
      chat_enabled: true,
    },
  },
  {
    name: 'sales-qualifier-basic',
    display: 'Sales Qualifier (Basic)',
    description:
      'Qualifier that captures needs / budget / timeline and delegates objection handling to a specialist.',
    category: 'sales',
    icon: 'cart',
    version: '1.0',
    definition: {
      entry_agent_name: 'lead-qualifier',
      agents: [
        {
          name: 'lead-qualifier',
          system_prompt:
            'You qualify inbound leads. Ask about needs, budget, decision authority, and timeline.',
          capabilities: [{ type: 'memory', config: {} }],
        },
        {
          name: 'objection-handler',
          system_prompt:
            'You address common sales objections (price, timing, competition) and return a concise rebuttal.',
          capabilities: [],
        },
      ],
      relations: [{ source: 'lead-qualifier', target: 'objection-handler' }],
      chat_enabled: true,
    },
  },
  {
    name: 'internal-hr-assistant',
    display: 'Internal HR Assistant',
    description:
      'HR front-desk agent that delegates policy lookups to a docs-search specialist.',
    category: 'internal',
    icon: 'building',
    version: '1.0',
    definition: {
      entry_agent_name: 'hr-assistant',
      agents: [
        {
          name: 'hr-assistant',
          system_prompt:
            'You answer employee HR questions. Delegate to the docs-search agent for specific policy lookups.',
          capabilities: [],
        },
        {
          name: 'docs-search',
          system_prompt:
            'You search the HR knowledge base and return cited policy text verbatim.',
          capabilities: [{ type: 'knowledge', config: {} }],
        },
      ],
      relations: [{ source: 'hr-assistant', target: 'docs-search' }],
      chat_enabled: true,
    },
  },
  {
    name: 'generic-hello-world',
    display: 'Generic Assistant',
    description:
      'Single-agent starter. A helpful assistant with a chat trigger — minimal scaffold for custom use cases.',
    category: 'generic',
    icon: 'sparkles',
    version: '1.0',
    definition: {
      entry_agent_name: 'assistant',
      agents: [
        {
          name: 'assistant',
          system_prompt:
            'You are a helpful assistant. Answer the user questions concisely and ask clarifying questions when needed.',
          capabilities: [],
        },
      ],
      relations: [],
      chat_enabled: true,
    },
  },
];
