# Contributing to SyntheticBrew

Thank you for your interest in contributing to SyntheticBrew! This guide will help you get started.

## Before You Start

1. **Open an issue first** — discuss your idea before writing code. This prevents wasted effort on PRs that won't be accepted.
2. **One PR = one change** — keep PRs small and focused. Bug fix, feature, or refactor — pick one per PR.
3. **Sign the CLA** — on your first PR, the CLA Assistant bot will ask you to sign the [Contributor License Agreement](CLA.md). This is required for all contributions.

## Development Setup

### Prerequisites

- Go 1.24+
- Node.js 20+
- PostgreSQL 16+ (or Docker)
- Make

### Quick Start

```bash
# Clone
git clone https://github.com/syntheticinc/syntheticbrew.git
cd syntheticbrew

# Backend
go mod download
go build ./...

# Admin Dashboard
cd admin
npm ci
npm run dev    # dev server with hot reload
cd ..

# Run tests
go test ./...
cd admin && npx vitest && cd ..
```

### Docker (full stack)

```bash
# Copy env template
cp .env.example .env
# Edit .env with your API keys

# Start everything
docker compose up -d

# Verify
curl http://localhost:8443/api/v1/health
```

## Architecture

SyntheticBrew follows **Clean Architecture** with strict layer separation:

```
cmd/ce/              Entry point (Community Edition)
internal/
  domain/            Pure entities — no external deps, no struct tags
  usecase/           Business logic + consumer-side interfaces
  service/           Reusable helpers (task worker, scheduler, etc.)
  infrastructure/    External integrations (DB, LLM, MCP, tools)
  delivery/          HTTP/gRPC handlers (thin adapters)
  app/               Application bootstrap and wiring
admin/               React/TypeScript admin dashboard
```

### Key Principles

- **Interfaces belong to consumers** — defined in the usecase file that needs them, not alongside the implementation
- **Domain is pure** — no GORM tags, no JSON tags, no framework imports
- **Delivery is thin** — handlers only transform request/response, no business logic
- **Early returns** — errors at the top, happy path at the bottom

## Code Style

### Go

- Follow standard Go conventions and `golangci-lint` rules (see `.golangci.yml`)
- Error handling: always wrap with context — `fmt.Errorf("create agent: %w", err)`
- Logging: use `slog.InfoContext`/`slog.ErrorContext` with structured fields
- No `goto`, no `else` after `return`, no ignored errors (`_ = err`)

### TypeScript (Admin Dashboard)

- React functional components with hooks
- TypeScript strict mode
- Tailwind CSS for styling

## Pull Request Process

1. Fork the repository and create a branch from `main`
2. Make your changes following the code style above
3. Ensure all checks pass:
   ```bash
   go build ./...
   go test ./...
   cd admin && npm run build && npx tsc --noEmit
   ```
4. Write a clear PR description explaining **what** and **why**
5. Link the related issue (`Closes #123`)
6. Request a review

### PR Checklist

- [ ] Issue exists and is linked
- [ ] `go build ./...` passes
- [ ] `go test ./...` passes
- [ ] `npm run build` passes in `admin/`
- [ ] New code has tests
- [ ] No hardcoded secrets or credentials
- [ ] Documentation updated if needed

### What We Look For

- Clean Architecture boundaries respected
- SOLID principles followed
- Tests cover the new/changed behavior
- No unnecessary complexity or premature abstractions

## Reporting Issues

Use [GitHub Issues](https://github.com/syntheticinc/syntheticbrew/issues) with the provided templates:
- **Bug Report** — include steps to reproduce, logs, environment
- **Feature Request** — describe the problem you're solving

## Security

Found a vulnerability? Please report it privately. See [SECURITY.md](SECURITY.md) for details.

## License

By contributing, you agree to the terms of our [Contributor License Agreement](CLA.md). Your contributions will be licensed under the [BSL 1.1](LICENSE) license. The CLA grants Synthetic AI Inc. the right to sublicense contributions, which is required for the BSL → Apache 2.0 conversion on the Change Date.
