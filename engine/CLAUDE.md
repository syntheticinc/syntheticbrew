# SyntheticBrew Server (Go)

## Stack
- Go 1.25+, HTTP REST + SSE chat
- Cloudwego Eino (ReAct agent framework)
- PostgreSQL + GORM, Viper + YAML config
- OpenAI-compatible API, slog logging

## Structure
```
syntheticinc/engine/
├── cmd/ce/                # Production entry point (Community Edition)
├── internal/
│   ├── domain/            # Pure entities (NO external deps, NO tags)
│   ├── usecase/           # Business logic + consumer-side interfaces
│   ├── service/           # Reusable helpers
│   ├── delivery/http/     # HTTP handlers (thin!)
│   └── infrastructure/    # DB, APIs, tools, agents
├── tests/prompt_regression/ # Prompt regression tests
└── logs/                  # Session logs + context snapshots
```

## Commands
```bash
go run ./cmd/ce                  # Start server
go test ./...                    # Unit tests
go test -tags prompt -v -timeout 300s ./tests/prompt_regression/...  # Prompt regression
```

## Go Code Style

### Early Returns (mandatory)
Errors first, happy path last. Flat structure.

### Forbidden
- goto — NEVER
- else after return — remove it
- Deep nesting — invert conditions
- Ignoring errors — `_ = err` is forbidden

### Error Handling
```go
if err != nil {
    return fmt.Errorf("create user: %w", err)
}
```

### Logging
```go
slog.InfoContext(ctx, "processing request", "user_id", userID)
slog.ErrorContext(ctx, "failed to save", "error", err)
```

## Testing

### Integration Tests (Level 1)
- `tests/integration/` — integration suite hitting the running engine via HTTP REST + SSE.

### Prompt Regression (Level 2)
- `tests/prompt_regression/fixtures/` — JSON fixtures from logs
- Build tag: `//go:build prompt`
- Fixtures from `logs/<session>/supervisor_step_N_context.json`

### Context Logger
- `internal/infrastructure/agents/context_logger.go`
- Logs LLM context to `logs/<session>/step_N_context.json`
