# ByteBrew Engine

[![Build](https://img.shields.io/github/actions/workflow/status/syntheticinc/bytebrew/chart-test.yml?branch=main&style=flat-square&logo=githubactions&logoColor=white&label=build)](https://github.com/syntheticinc/bytebrew/actions/workflows/chart-test.yml)
[![License](https://img.shields.io/badge/license-BSL%201.1-blue?style=flat-square)](LICENSE)
[![Go Version](https://img.shields.io/github/go-mod/go-version/syntheticinc/bytebrew?filename=engine%2Fgo.mod&style=flat-square&logo=go&logoColor=white&label=go)](engine/go.mod)
[![Go Report Card](https://goreportcard.com/badge/github.com/syntheticinc/bytebrew/engine?style=flat-square)](https://goreportcard.com/report/github.com/syntheticinc/bytebrew/engine)
[![Release](https://img.shields.io/github/v/release/syntheticinc/bytebrew?style=flat-square&logo=github&label=release)](https://github.com/syntheticinc/bytebrew/releases/latest)
[![Docker Pulls](https://img.shields.io/docker/pulls/bytebrew/engine?style=flat-square&logo=docker&logoColor=white)](https://hub.docker.com/r/bytebrew/engine)

**Open-source AI agent runtime.** Build, deploy, and orchestrate autonomous AI agents with multi-agent coordination, MCP tool integration, and a visual admin dashboard.

> Not another AI chatbot. ByteBrew is the agent brewery.

## Features

- **Multi-Agent Orchestration** — agents spawn and coordinate with each other via ReAct framework
- **MCP Tool Ecosystem** — connect any Model Context Protocol server (stdio, SSE, HTTP, WebSocket, Docker)
- **Visual Admin Dashboard** — configure agents, models, schemas, and tools from a web UI
- **Task System** — async background tasks with priorities, dependencies, and approval gates
- **Knowledge Base / RAG** — vector search over uploaded documents with pgvector
- **Agent Memory** — cross-session persistent memory per agent
- **Headless API** — REST + SSE chat for any frontend (web, mobile, CLI). No proprietary clients.
- **BYOK** — bring your own keys for any OpenAI-compatible LLM provider
- **Self-Hosted** — deploy on your infrastructure with Docker, Kubernetes, or bare metal

## Quick Start

```bash
# Start with Docker Compose
curl -fsSL https://bytebrew.ai/releases/docker-compose.yml -o docker-compose.yml
docker compose up -d

# Open admin dashboard
open http://localhost:8443/admin/
# Default credentials: admin / changeme
```

Or build from source:

```bash
cd engine
go build -o bytebrew ./cmd/ce
./bytebrew
```

## Configuration

ByteBrew can be configured via:

| Method | Use Case |
|--------|----------|
| **Environment variables** | Docker, Kubernetes, CI/CD |
| **config.yaml** | Local development, bare metal |
| **Admin Dashboard** | Visual configuration at `/admin/` |

Key environment variables:

```bash
DATABASE_URL=postgresql://user:pass@host:5432/bytebrew
ADMIN_USER=admin
ADMIN_PASSWORD=changeme
```

LLM provider, model and API key are configured through the onboarding
wizard on first launch (or later via Admin → Models). Engine does not
read LLM credentials from env or config files.

## Architecture

ByteBrew follows Clean Architecture with strict layer separation. All Go code lives under `engine/`:

```
engine/
  cmd/ce/              Community Edition entry point
  internal/
    domain/            Pure domain entities
    usecase/           Business logic + consumer-side interfaces
    service/           Task worker, scheduler, completion hooks
    infrastructure/    DB, LLM, MCP, agents, tools
    delivery/          HTTP handlers
    app/               Application bootstrap
  admin/               React/TypeScript admin dashboard
  widget/              Embeddable chat widget
  deploy/              Docker, Helm, systemd assets
```

## Deployment

| Method | Guide |
|--------|-------|
| **Docker Compose** | See [Quick Start](#quick-start) above |
| **Kubernetes** | Helm chart in [`engine/deploy/helm/`](engine/deploy/helm/) |
| **Bare Metal** | Binary + systemd + PostgreSQL + Caddy/nginx |

## Documentation

- **Website:** https://bytebrew.ai
- **Docs:** https://bytebrew.ai/docs/
- **API Reference:** https://bytebrew.ai/docs/api/

## Contributing

We welcome contributions! Please read [CONTRIBUTING.md](CONTRIBUTING.md) before submitting a PR.

- [Code of Conduct](CODE_OF_CONDUCT.md)
- [Security Policy](SECURITY.md)

## License

Licensed under [Business Source License 1.1](LICENSE). Contact [info@bytebrew.ai](mailto:info@bytebrew.ai) for alternative licensing.
