# SyntheticBrew SEO keyword map

Research date: 2026-07-14. Market: United States, English. Source: connected Ahrefs MCP. Volumes and keyword difficulty are directional and change over time.

The current domain returned no ranking US keywords in the Ahrefs organic-keyword report. The strategy therefore starts with specific, lower-difficulty product and solution intent while building internal links toward broader platform terms.

| Page | Primary keyword | US volume | KD | Supporting intent |
|---|---:|---:|---:|---|
| `/` | AI agent platform | 2,200 | 46 | AI agent builder, MCP integration, self-hosted AI |
| `/features/ai-agent-builder` | AI agent builder | 1,900 | 25 | no-code AI agent builder (700, KD 12) |
| `/features/multi-agent-orchestration` | multi-agent orchestration | 800 | 13 | multi-agent orchestration platform (250, KD 11) |
| `/features/mcp-integration` | MCP integration | 300 | 71 | build MCP server (1,300, KD 15), MCP tools (1,100, KD 43) |
| `/features/agent-memory` | AI agent memory | 400 | 39 | persistent agent memory |
| `/features/agentic-rag` | agentic RAG | 2,800 | 37 | what is agentic RAG (600, KD 17), AI knowledge base (1,100, KD 50) |
| `/knowledge-graphs` | knowledge graph LLM | 150 | 8 | grounded retrieval, LLM knowledge graph |
| `/features/ai-agent-observability` | AI agent observability | 400 | 16 | AI agent observability best practices (700, KD 12) |
| `/features/ai-agent-security` | AI agent security | 1,100 | 21 | AI agent security risks (250, KD 14) |
| `/features/product-integrations` | AI agent API | 200 | 7 | AI integration platform (200, KD 5) |
| `/self-hosted` | self-hosted AI | 450 | 3 | self-hosted AI chatbot (90, KD 1) |
| `/build` | AI agent development services | 1,300 | 14 | custom AI agent development services (300, KD 9) |
| `/enterprise` | enterprise AI agents | 1,200 | 7 | secure AI agents, AI data residency |
| `/solutions/customer-service` | AI agents for customer service | 500 | 28 | AI customer support agent |
| `/solutions/ecommerce` | AI agents for ecommerce | 300 | 3 | ecommerce AI agent |
| `/solutions/banking` | AI agents for banking | 250 | n/a | banking AI agent |
| `/solutions/manufacturing-iot` | AI agents for manufacturing | 300 | 0 | AI agents for IoT, AIoT platform |
| `/solutions/saas-product-teams` | AI agents for SaaS | low | n/a | AI product integration, enterprise AI agents |

### 2026-07-14 keyword rework (Ahrefs-validated; DR 0 → target KD ≤ 12 in H1/H2)

Homepage retargeted from "AI agent platform" (2,200 / KD 46 — unwinnable at DR 0) to winnable terms; comparison cluster and blog added.

| Page | Primary keyword | US volume | KD | Notes |
|---|---|---:|---:|---|
| `/` | open source AI agents + AI agent infrastructure | 450 + 300 | low / 10 | H2s: AI agent orchestration (1,000/7), how to integrate AI into an app (300/4), self-hosted AI (450/3), AI agent platform comparison (90/12) |
| `/compare/langchain` | langchain alternatives | 500 | 3 | retargeted from generic "vs" |
| `/compare/n8n` | n8n alternatives / n8n alternative | 1,200 + 600 | 7 / 5 | new page |
| `/compare/crewai` | crewai alternatives + crewai vs langchain | 150 + 150 | n/a | new page |
| `/compare/flowise` | flowise alternative(s) | 130 | n/a | new page |
| `/compare/langflow` | langflow alternatives | 80 | n/a | new page |
| `/blog/dify-alternative-open-source-ai-agents` | dify alternative(s) | 60 | n/a | supporting: dify ai (1,600/21), dify pricing (200), what is dify (150), dify vs n8n (200), best ai agent platform (450/11) |
| `/blog/langchain-vs-langgraph` | langchain vs langgraph + langgraph vs langchain | 2,600 + 1,500 | 7 | traffic piggyback → CTA + /compare/langchain |
| `/build` | AI agent development services | 2,000 | 9 | H1 front-loaded; custom AI agent development (800/12) in H2 |
| `/enterprise` | enterprise AI agents | 1,300 | 9 | H1 front-loaded |

### 2026-07-14 second validation batch (heading audit + big-term strategy)

Validated and placed:
| Keyword | US vol | KD | Placement |
|---|---:|---:|---|
| ai agent use cases | 700 | 11 | homepage solutions H2, SolutionPage template H2s |
| best ai agent platform | 450 | 11 | `/compare` hub (with ai agent platform comparison 90/12) |
| ai agent capabilities | 150 | low | homepage features-grid H2 |
| secure ai agents | 250 | low | SolutionPage control H2, enterprise |
| how to build an ai agent (+ variants) | 3,800+1,800+450 | 24–33 | dedicated article `/blog/how-to-build-an-ai-agent` (KD 24 reachable in months; earns links) |
| what are ai agents / types of ai agents | 6,400 / 1,100 | 79 / 46 | pillar `/blog/what-are-ai-agents` — long play + internal-link hub; expected to win long-tail first |

Rejected for H1/H2 on money pages (do not re-propose): what are ai agents (KD 79), ai agents for business (52), ai agent tools (66), agent memory (54 — use "AI agent memory" 400/39 as the established term), ai agent examples (31), build ai agents (33) — high-KD terms live in body text and their own articles, never in money-page heading slots at DR 0.

Rule enforced this round: every H1/H2 site-wide carries a validated phrase (footer column titles demoted from h2 to span; FeaturePage/SolutionPage/ComparePage template H2s now keyword-templated per page). Sales-voice headings allowed only on: Kilo proof panel, bottom CTAs.

## Content rules implemented

- One indexable intent per page and one visible H1.
- Primary phrase appears naturally in title, H1, introduction, or first major H2.
- Page titles are capped at 60 characters; descriptions are capped at 160.
- Keywords are present in metadata for the requested page inventory, while ranking work relies on useful copy and internal links rather than keyword stuffing.
- Feature and solution pages cross-link to adjacent intents.
- Industry pages describe possible architectures. They do not manufacture customer claims or anonymous proof.
- Kilo AIoT is the only named production deployment used at launch.

## Recheck cadence

Run Ahrefs rank tracking and a crawl after release, then review impressions and queries in Google Search Console after 4–8 weeks. Re-evaluate intent and internal links before adding new pages. First-page rankings cannot be guaranteed; they depend on authority, competition, content quality, technical health, and earned links over time.
