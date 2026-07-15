export const SITE = {
  name: 'SyntheticBrew',
  url: 'https://syntheticbrew.ai',
  github: 'https://github.com/syntheticinc/syntheticbrew',
  calendly: 'https://calendly.com/timkrav/metting-with-tim-chirp',
  cloud: '/register',
  selfHost: '/docs/getting-started/quick-start/',
  email: 'info@syntheticbrew.ai',
} as const;

export const PRIMARY_CTA = [
  { label: 'Try Cloud', href: SITE.cloud, kind: 'primary', event: 'cloud_signup_start' },
  { label: 'Self-host', href: SITE.selfHost, kind: 'secondary', event: 'self_host_quickstart' },
  { label: 'Contact sales', href: SITE.calendly, kind: 'quiet', event: 'contact_sales_start', external: true },
] as const;

export type Feature = {
  slug: string;
  path: string;
  shortTitle: string;
  eyebrow: string;
  title: string;
  description: string;
  h1: string;
  lede: string;
  keywords: string[];
  screenshot: string;
  screenshotAlt: string;
  outcomes: { title: string; text: string }[];
  workflow: { title: string; text: string }[];
  callout: string;
};

export const FEATURES: Feature[] = [
  {
    slug: 'ai-agent-builder', path: '/features/ai-agent-builder/', shortTitle: 'AI Agent Builder', eyebrow: 'Visual and prompt-driven',
    title: 'No-Code AI Agent Builder | SyntheticBrew',
    description: 'Build production AI agents from a prompt or visual canvas. Configure tools, memory, flows, and models without stitching together agent plumbing.',
    h1: 'AI agent builder for production systems',
    lede: 'Describe the outcome in plain English. SyntheticBrew proposes agents, tools, flows, gates, and memory—then lets your team inspect and refine every part in a visual canvas.',
    keywords: ['AI agent builder', 'no-code AI agent builder', 'visual AI agent builder', 'build AI agents'],
    screenshot: '/screenshots/canvas-with-ai-builder.png', screenshotAlt: 'SyntheticBrew visual AI agent builder canvas with connected agents and tools',
    outcomes: [
      { title: 'Start with intent', text: 'Turn a plain-language brief into a working schema instead of starting from blank configuration.' },
      { title: 'ReAct reasoning', text: 'Agents reason, act, observe tool results, and repeat until the task is complete or a defined boundary is reached.' },
      { title: 'Keep engineering control', text: 'Review model choices, tool access, memory scope, and flow edges before anything reaches users.' },
      { title: 'Iterate in one place', text: 'Test in chat, inspect each reasoning step, then refine the system without changing frameworks.' },
    ],
    workflow: [
      { title: 'Describe', text: 'Explain what the agent should accomplish and what boundaries it must respect.' },
      { title: 'Generate', text: 'The builder assembles a supervisor, specialists, tools, gates, and memory.' },
      { title: 'Ship', text: 'Expose the agent through REST, SSE, the web client, or an embeddable widget.' },
    ],
    callout: 'The builder itself runs as a SyntheticBrew agent—the platform uses its own runtime end to end.',
  },
  {
    slug: 'multi-agent-orchestration', path: '/features/multi-agent-orchestration/', shortTitle: 'Multi-Agent Orchestration', eyebrow: 'Specialists that work together',
    title: 'Multi-Agent Orchestration Platform | SyntheticBrew',
    description: 'Coordinate supervisor and specialist AI agents with scoped tools, gates, loops, delegation, and observable handoffs in one production runtime.',
    h1: 'Multi-agent orchestration without custom plumbing',
    lede: 'Build teams of specialized agents that delegate work, use narrowly scoped tools, pass results through gates, and stream their progress to your product.',
    keywords: ['multi-agent orchestration', 'multi-agent orchestration platform', 'AI agent orchestration', 'agent workflows'],
    screenshot: '/screenshots/chat-with-tools.png', screenshotAlt: 'SyntheticBrew chat showing agent delegation and server-side tool calls',
    outcomes: [
      { title: 'Clear responsibility', text: 'Assign each specialist a focused role and only the tools it should be allowed to call.' },
      { title: 'Controlled handoffs', text: 'Connect agents with transfer, spawn, gates, and loops instead of hiding coordination in prompts.' },
      { title: 'Visible execution', text: 'Stream delegation, tool calls, tool results, and final output as structured SSE events.' },
      { title: 'Background automation', text: 'Dispatch long-running agent work as background tasks, track it through the task system, and validate output through gates.' },
    ],
    workflow: [
      { title: 'Define roles', text: 'Configure the supervisor and the specialists available for delegation.' },
      { title: 'Scope access', text: 'Bind only the MCP servers and tools each agent needs.' },
      { title: 'Observe', text: 'Follow agent spawn and result events in real time and audit the completed run.' },
    ],
    callout: 'Per-agent tool scoping creates a real security boundary: a rules agent does not automatically gain access to organization or billing tools.',
  },
  {
    slug: 'mcp-integration', path: '/features/mcp-integration/', shortTitle: 'MCP Integration', eyebrow: 'Connect tools once',
    title: 'MCP Integration for AI Agents | SyntheticBrew',
    description: 'Connect AI agents to MCP servers and internal APIs with server-side execution, forwarded auth context, scoped tools, and human confirmation.',
    h1: 'MCP integration that is ready for real users',
    lede: 'Register MCP servers, assign tools to the right agents, forward the user’s auth context, and execute every call on the server—without turning your frontend into an orchestrator.',
    keywords: ['MCP integration', 'MCP server', 'MCP tools', 'build MCP server', 'Model Context Protocol'],
    screenshot: '/screenshots/admin/mcp-servers.png', screenshotAlt: 'SyntheticBrew admin dashboard for configuring MCP servers',
    outcomes: [
      { title: 'Simple rollout', text: 'Add MCP tools to an existing agent without rebuilding the chat UI or reasoning loop.' },
      { title: 'Auth stays attached', text: 'Forward Authorization, organization, user, and custom tenant headers to your own tool servers.' },
      { title: 'Risk-aware actions', text: 'Pause destructive calls for explicit user confirmation before execution.' },
    ],
    workflow: [
      { title: 'Connect', text: 'Register an HTTP, SSE, streamable-HTTP, or stdio MCP server—including Docker-launched servers over stdio.' },
      { title: 'Assign', text: 'Expose only the relevant tool set to each agent.' },
      { title: 'Run', text: 'SyntheticBrew executes tools server-side and returns structured progress to any client.' },
    ],
    callout: 'Your product remains a thin HTTP and SSE client. Tool selection, execution, retries, and results stay inside the runtime.',
  },
  {
    slug: 'agent-memory', path: '/features/agent-memory/', shortTitle: 'AI Agent Memory', eyebrow: 'Context across sessions',
    title: 'Persistent AI Agent Memory | SyntheticBrew',
    description: 'Give AI agents persistent, schema-isolated memory across conversations while keeping session history, scope, and deployment under your control.',
    h1: 'AI agent memory that persists with purpose',
    lede: 'Let agents remember customers, decisions, and working context across sessions while keeping memory isolated by schema and stored in your PostgreSQL deployment.',
    keywords: ['AI agent memory', 'persistent agent memory', 'LLM memory', 'conversation memory'],
    screenshot: '/screenshots/admin-agent-detail.png', screenshotAlt: 'SyntheticBrew agent detail screen with memory and model configuration',
    outcomes: [
      { title: 'Continuity', text: 'Resume useful context across requests instead of replaying an entire history on every turn.' },
      { title: 'Isolation', text: 'Scope memory per schema so one agent system does not leak context into another.' },
      { title: 'Ownership', text: 'Store session history in PostgreSQL in your Cloud or self-hosted environment.' },
    ],
    workflow: [
      { title: 'Choose scope', text: 'Define which schema and agent may access persistent memory.' },
      { title: 'Run sessions', text: 'The API maintains conversation state using stable session identifiers.' },
      { title: 'Continue', text: 'Return users to the right context across requests and product surfaces.' },
    ],
    callout: 'Memory is part of the runtime, not an SDK exercise left for every application team to rebuild.',
  },
  {
    slug: 'agentic-rag', path: '/features/agentic-rag/', shortTitle: 'Agentic RAG', eyebrow: 'Ground answers in evidence',
    title: 'Agentic RAG & AI Knowledge Base | SyntheticBrew',
    description: 'Ground AI agents in PDFs, DOCX, Markdown, and CSV knowledge. Combine agentic RAG with tools and knowledge graphs in one runtime.',
    h1: 'Agentic RAG for answers grounded in your knowledge',
    lede: 'Upload your documents, isolate knowledge per schema, and let agents retrieve evidence automatically while reasoning and using tools.',
    keywords: ['agentic RAG', 'AI knowledge base', 'RAG agents', 'knowledge base software'],
    screenshot: '/screenshots/knowledge.png', screenshotAlt: 'SyntheticBrew knowledge base with uploaded sources for agentic RAG',
    outcomes: [
      { title: 'Faster grounding', text: 'Bring PDF, DOCX, Markdown, text, and CSV knowledge into the same platform as your agents.' },
      { title: 'Automatic retrieval', text: 'Agents search relevant sources as part of their reasoning instead of relying only on prompt context.' },
      { title: 'Right retrieval mode', text: 'Use vector RAG for narrative evidence and knowledge graphs for typed entities and relationships.' },
    ],
    workflow: [
      { title: 'Ingest', text: 'Add approved documents to a schema-specific knowledge base.' },
      { title: 'Retrieve', text: 'Agents select relevant passages while solving the user’s request.' },
      { title: 'Answer', text: 'Combine retrieved evidence with tool results and an explicit reasoning flow.' },
    ],
    callout: 'Narrative documents and structured domain records need different retrieval strategies. SyntheticBrew supports both.',
  },
  {
    slug: 'knowledge-graphs', path: '/knowledge-graphs/', shortTitle: 'Knowledge Graphs', eyebrow: 'Typed, deterministic retrieval',
    title: 'Knowledge Graphs for LLMs | SyntheticBrew',
    description: 'Give LLM agents typed entities, relationships, deterministic retrieval, and auto-generated MCP tools. Ground domain answers without invented IDs.',
    h1: 'Knowledge graphs for LLMs that must get facts right',
    lede: 'Declare categories, attributes, and relationships as a typed domain model. SyntheticBrew generates MCP tools that let agents retrieve exact records instead of guessing identifiers.',
    keywords: ['knowledge graph LLM', 'LLM knowledge graph', 'AI knowledge graph', 'grounded retrieval'],
    screenshot: '/screenshots/schemas-list.png', screenshotAlt: 'SyntheticBrew schema list for typed knowledge graph definitions',
    outcomes: [
      { title: 'Typed domain truth', text: 'Model entities, attributes, and relationships explicitly instead of flattening everything into text chunks.' },
      { title: 'Deterministic tools', text: 'Auto-generate MCP retrieval tools from the schema and keep identifiers grounded in real records.' },
      { title: 'Better refusal', text: 'When the requested entity does not exist, the agent can stop instead of manufacturing an answer.' },
    ],
    workflow: [
      { title: 'Model', text: 'Declare your domain taxonomy, attributes, and relationships.' },
      { title: 'Generate', text: 'Expose deterministic search and traversal as scoped MCP tools.' },
      { title: 'Ground', text: 'Require agents to resolve real entities before making claims or taking action.' },
    ],
    callout: 'Use RAG for narrative evidence. Use a knowledge graph when exact entities, IDs, relationships, and full recall matter.',
  },
  {
    slug: 'ai-agent-observability', path: '/features/ai-agent-observability/', shortTitle: 'Agent Observability', eyebrow: 'See every step',
    title: 'AI Agent Observability & Audit | SyntheticBrew',
    description: 'Trace AI agent reasoning, delegation, tool calls, failures, and results with structured events, audit logs, health checks, and Prometheus metrics.',
    h1: 'AI agent observability from prompt to tool result',
    lede: 'Follow reasoning, sub-agent delegation, tool calls, results, errors, and completion in real time—then retain an operational record for debugging and review.',
    keywords: ['AI agent observability', 'agent observability best practices', 'LLM observability', 'AI audit logs'],
    screenshot: '/screenshots/admin/audit-log.png', screenshotAlt: 'SyntheticBrew immutable audit log for AI agent actions',
    outcomes: [
      { title: 'Live execution', text: 'Render structured SSE events as progress instead of showing users an unexplained spinner.' },
      { title: 'Operational evidence', text: 'Review actions, failures, parameters, and actor context in the audit trail.' },
      { title: 'Production signals', text: 'Use health endpoints and Prometheus metrics alongside your existing monitoring stack.' },
      { title: 'Recovery and resilience', text: 'Per-tool execution timeouts and automatic circuit breakers isolate failing MCP servers and models before they degrade your product.' },
    ],
    workflow: [
      { title: 'Stream', text: 'Receive typed thinking, tool, delegation, confirmation, error, and done events.' },
      { title: 'Inspect', text: 'Debug an agent run in the playground or audit log.' },
      { title: 'Monitor', text: 'Track runtime health and service metrics in production.' },
    ],
    callout: 'Observability is built into the execution protocol, so product UI, operators, and developers see the same underlying run.',
  },
  {
    slug: 'ai-agent-security', path: '/features/ai-agent-security/', shortTitle: 'Agent Security', eyebrow: 'Boundaries before autonomy',
    title: 'AI Agent Security & Tool Controls | SyntheticBrew',
    description: 'Secure AI agents with scoped tools, forwarded identity, Ed25519 JWTs, confirmation gates, audit logs, BYOK, and separated control and data planes.',
    h1: 'AI agent security built around tool boundaries',
    lede: 'Treat every tool call as a privileged operation. Scope access per agent, forward the user’s identity to your backend, and require confirmation before consequential actions.',
    keywords: ['AI agent security', 'AI agent security risks', 'secure AI agents', 'agent tool security'],
    screenshot: '/screenshots/admin/api-keys.png', screenshotAlt: 'SyntheticBrew API key management for secure agent access',
    outcomes: [
      { title: 'Least privilege', text: 'Give each specialist only its designated tools and MCP servers.' },
      { title: 'End-user context', text: 'Forward JWT, organization, user, and tenant headers to the system that enforces RBAC.' },
      { title: 'Human checkpoints', text: 'Mark destructive tools so execution pauses until the user explicitly approves or rejects.' },
    ],
    workflow: [
      { title: 'Authenticate', text: 'Validate Ed25519-signed tokens or self-hosted API tokens.' },
      { title: 'Authorize', text: 'Scope tools per agent and enforce tenant access inside your domain API.' },
      { title: 'Confirm', text: 'Gate create, modify, delete, or otherwise consequential operations.' },
    ],
    callout: 'SyntheticBrew forwards identity; your backend remains the source of truth for permissions and tenant isolation.',
  },
  {
    slug: 'product-integrations', path: '/features/product-integrations/', shortTitle: 'Product Integrations', eyebrow: 'Headless by design',
    title: 'AI Agent API & Product Integration | SyntheticBrew',
    description: 'Add AI agents to web, mobile, SaaS, and IoT products through REST and SSE, a built-in chat client, or an embeddable widget.',
    h1: 'AI agent API for your existing product',
    lede: 'Keep your frontend and domain APIs. Add SyntheticBrew as a headless agent runtime through standard HTTP and structured server-sent events.',
    keywords: ['AI agent API', 'AI integration platform', 'embed AI agent', 'AI product integration'],
    screenshot: '/screenshots/widget-generator.png', screenshotAlt: 'SyntheticBrew embeddable AI chat widget generator',
    outcomes: [
      { title: 'Any frontend', text: 'Consume HTTP and SSE from React, Vue, mobile, or any client that can stream a response.' },
      { title: 'Fast embed', text: 'Generate a Shadow DOM-isolated chat widget and add it with one script tag.' },
      { title: 'Rich progress', text: 'Render tool calls, confirmations, sub-agent work, tables, and final answers as native UI.' },
    ],
    workflow: [
      { title: 'Send', text: 'POST a message and optional session ID to the agent chat endpoint.' },
      { title: 'Stream', text: 'Parse structured SSE events for progress, output, confirmations, and errors.' },
      { title: 'Embed', text: 'Use your own UI, the included web client, or the generated widget.' },
    ],
    callout: 'No client-side tool executor or proprietary SDK is required. The runtime handles orchestration server-side.',
  },
  {
    slug: 'self-hosted', path: '/self-hosted/', shortTitle: 'Self-Hosted AI', eyebrow: 'Your infrastructure, your keys',
    title: 'Self-Hosted Open-Source AI Agents | SyntheticBrew',
    description: 'Run open-source AI agents on your infrastructure with Docker, Kubernetes, or a binary. Bring any LLM key and keep data under your control.',
    h1: 'Self-hosted AI agents without the infrastructure project',
    lede: 'Deploy the complete agent runtime with your own PostgreSQL, model keys, network controls, and operational stack—without rebuilding orchestration, memory, or tool execution.',
    keywords: ['self-hosted AI', 'self-hosted AI chatbot', 'open-source AI agents', 'on-premise AI'],
    screenshot: '/screenshots/admin/health.png', screenshotAlt: 'SyntheticBrew self-hosted engine health dashboard',
    outcomes: [
      { title: 'Data control', text: 'Run the runtime and PostgreSQL inside your infrastructure and security perimeter.' },
      { title: 'Model freedom', text: 'Use OpenAI, Anthropic, Google Gemini, Azure OpenAI, Ollama, OpenRouter, or any OpenAI-compatible provider such as DeepSeek and Mistral.' },
      { title: 'No runtime meter', text: 'Community Edition has no per-agent, step, session, or storage limits from SyntheticBrew.' },
    ],
    workflow: [
      { title: 'Install', text: 'Start with Docker Compose, Kubernetes with Helm, or a standalone binary.' },
      { title: 'Configure', text: 'Set models, agents, MCP servers, tools, schemas, and network boundaries.' },
      { title: 'Operate', text: 'Use health checks, logs, the admin dashboard, and production monitoring.' },
    ],
    callout: 'Community Edition is BSL 1.1 and automatically converts to Apache 2.0 after four years.',
  },
];

export type Solution = {
  slug: string;
  path: string;
  label: string;
  title: string;
  description: string;
  h1: string;
  lede: string;
  keywords: string[];
  challenge: string;
  uses: { title: string; text: string }[];
  architecture: string[];
  screenshot: string;
  screenshotAlt: string;
  proof?: { label: string; title: string; text: string; href: string };
};

export const SOLUTIONS: Solution[] = [
  {
    slug: 'customer-service', path: '/solutions/customer-service/', label: 'Customer service',
    title: 'AI Agents for Customer Service | SyntheticBrew',
    description: 'Build customer service AI agents that use approved knowledge, remember context, call support tools, and hand consequential actions to a person.',
    h1: 'AI agents for customer service that can do more than answer',
    lede: 'Combine grounded answers, session memory, support-system tools, and clear confirmation or escalation points in one service experience.',
    keywords: ['AI agents for customer service', 'AI customer support agent', 'customer service automation'],
    challenge: 'Support automation loses trust when it invents policy, forgets context, or changes customer data without a clear checkpoint.',
    uses: [
      { title: 'Grounded answers', text: 'Retrieve from approved product, policy, and troubleshooting knowledge before responding.' },
      { title: 'Account-aware help', text: 'Call scoped MCP tools with the customer’s authenticated context to look up relevant records.' },
      { title: 'Safe resolution', text: 'Require confirmation for refunds, cancellations, or changes, and route exceptions to a person.' },
    ],
    architecture: ['Your support UI or SyntheticBrew widget', 'SyntheticBrew agent runtime and session memory', 'Knowledge base plus scoped MCP support tools', 'Your CRM, ticketing, and account APIs'],
    screenshot: '/screenshots/widget-generator.png', screenshotAlt: 'SyntheticBrew customer service chat widget configuration',
  },
  {
    slug: 'ecommerce', path: '/solutions/ecommerce/', label: 'Ecommerce',
    title: 'AI Agents for Ecommerce | SyntheticBrew',
    description: 'Use AI agents for ecommerce discovery, order support, and guided actions with product knowledge, live tools, memory, and approval gates.',
    h1: 'AI agents for ecommerce journeys grounded in live data',
    lede: 'Help shoppers discover products, understand policies, and resolve order questions using approved knowledge and your current commerce APIs.',
    keywords: ['AI agents for ecommerce', 'ecommerce AI agent', 'AI shopping assistant'],
    challenge: 'A useful commerce agent needs both narrative product knowledge and live inventory, customer, and order context—without crossing authorization boundaries.',
    uses: [
      { title: 'Product discovery', text: 'Combine catalog attributes with narrative guidance to narrow options without inventing availability.' },
      { title: 'Order support', text: 'Look up order status through authenticated, tenant-aware tools.' },
      { title: 'Guided actions', text: 'Prepare changes or returns and ask for confirmation before consequential calls.' },
    ],
    architecture: ['Storefront, app, or embedded widget', 'Supervisor plus catalog and order specialists', 'Product knowledge and MCP commerce tools', 'Catalog, inventory, order, and customer systems'],
    screenshot: '/screenshots/chat-with-tools.png', screenshotAlt: 'SyntheticBrew agent chat using live product tools',
  },
  {
    slug: 'banking', path: '/solutions/banking/', label: 'Banking',
    title: 'AI Agents for Banking Workflows | SyntheticBrew',
    description: 'Explore secure AI agents for banking workflows with self-hosted deployment, scoped tools, grounded knowledge, identity forwarding, and audit trails.',
    h1: 'How AI agents can support banking workflows',
    lede: 'Design assistants for policy navigation, operations, and internal service workflows while keeping deployment, identity, tool scope, and confirmation under institutional control.',
    keywords: ['AI agents for banking', 'banking AI agent', 'AI in banking workflows'],
    challenge: 'Banking use cases need strict grounding, permission-aware access, traceable actions, and deployment choices that match institutional risk controls.',
    uses: [
      { title: 'Policy assistance', text: 'Retrieve approved procedures and cite the underlying knowledge used to form an answer.' },
      { title: 'Operations support', text: 'Delegate narrowly defined tasks to specialists with separate tool access.' },
      { title: 'Controlled action', text: 'Forward identity to the system of record and pause high-impact calls for confirmation.' },
    ],
    architecture: ['Internal banking application', 'Self-hosted SyntheticBrew runtime', 'Scoped specialists, knowledge, audit, and confirmation', 'Institutional APIs that enforce RBAC and tenant boundaries'],
    screenshot: '/screenshots/admin/audit-log.png', screenshotAlt: 'SyntheticBrew audit trail suitable for reviewing controlled banking agent workflows',
  },
  {
    slug: 'manufacturing-iot', path: '/solutions/manufacturing-iot/', label: 'Manufacturing & IoT',
    title: 'AI Agents for Manufacturing & IoT | SyntheticBrew',
    description: 'Connect AI agents to manufacturing and IoT data with MCP tools, domain grounding, permission-scoped actions, confirmations, and live event streaming.',
    h1: 'AI agents for manufacturing and IoT operations',
    lede: 'Put an agent over real device and operational data, let specialists use domain tools, and preserve a human checkpoint before consequential changes.',
    keywords: ['AI agents for manufacturing', 'AI agents for IoT', 'industrial AI agents', 'AIoT platform'],
    challenge: 'Operational systems require agents to understand domain structure, read current state, respect access controls, and verify the result of every write.',
    uses: [
      { title: 'Fleet understanding', text: 'Answer permission-scoped questions from current device and telemetry data.' },
      { title: 'Operational configuration', text: 'Draft rules, alarms, and escalation logic through domain-specific tools.' },
      { title: 'Confirmed execution', text: 'Pause before consequential actions, execute after approval, and read the result back.' },
    ],
    architecture: ['Operations UI and authenticated user', 'SyntheticBrew supervisor and domain specialists', 'MCP tools with forwarded organization context', 'Device, rules, alarm, and analytics services'],
    screenshot: '/screenshots/chat-with-tools.png', screenshotAlt: 'SyntheticBrew agent using scoped tools in a production workflow',
    proof: {
      label: 'Named production deployment', title: 'SyntheticBrew powers the AI layer in Kilo AIoT',
      text: 'Kilo uses SyntheticBrew as the agent runtime behind permission-scoped device questions, rule and alarm workflows, confirmation before consequential actions, and result verification after writes.',
      href: 'https://kilo.ai',
    },
  },
  {
    slug: 'saas-product-teams', path: '/solutions/saas-product-teams/', label: 'SaaS & product teams',
    title: 'AI Agents for SaaS Products | SyntheticBrew',
    description: 'Add production AI agents to SaaS products through REST and SSE with multi-tenant context, scoped tools, memory, RAG, and an embeddable widget.',
    h1: 'AI agents for SaaS and product teams',
    lede: 'Add agent capabilities to the product you already have while keeping your frontend, auth, domain logic, model choices, and deployment strategy.',
    keywords: ['AI agents for SaaS', 'AI agent platform', 'AI product integration', 'enterprise AI agents'],
    challenge: 'Product teams often prove an agent demo quickly, then lose months rebuilding runtime, tool execution, memory, tenancy, recovery, and observability.',
    uses: [
      { title: 'Headless integration', text: 'Connect any web or mobile product through REST and structured SSE.' },
      { title: 'Tenant-aware tools', text: 'Carry authorization and organization context into your domain APIs.' },
      { title: 'Choose the operating model', text: 'Use Cloud, self-host Community Edition, or ask SyntheticBrew to deliver the implementation.' },
    ],
    architecture: ['Your existing product frontend', 'SyntheticBrew REST, SSE, sessions, and orchestration', 'Knowledge, memory, and scoped MCP tools', 'Your multi-tenant application services'],
    screenshot: '/screenshots/canvas-with-ai-builder.png', screenshotAlt: 'SyntheticBrew visual canvas for SaaS product agent orchestration',
  },
];

export const FEATURE_MAP = Object.fromEntries(FEATURES.map((item) => [item.slug, item])) as Record<string, Feature>;
