# API Contracts — PATCH, Resolvers, and Kind-Split Models

## Overview

SyntheticBrew Engine REST API follows these contract patterns:

- **PATCH semantics** — partial updates with atomic guarantees
- **Resolvers** — UUID or name-based resource lookup
- **Kind-split models** — explicit `kind` field for model type validation

This document defines the contracts and expected behavior for each pattern.

---

## PATCH Semantics

### Specification

PATCH requests apply partial updates to a resource. The request body contains only the fields to update; unspecified fields are not modified.

**Contract:**
- Request: JSON object with subset of fields
- Response: Complete updated resource (all fields)
- Idempotent: Multiple identical PATCH requests produce same result
- Atomic: Update succeeds or fails entirely (no partial updates on conflict)
- Non-destructive: Omitted fields are preserved

### Example: Update Agent

```bash
# Current agent state
GET /api/v1/agents/my-agent
{
  "id": "agent_123",
  "name": "my-agent",
  "model_id": "model_456",
  "system_prompt": "You are helpful.",
  "max_turn_steps": 10,
  "public": false,
  "config": {...}
}

# Partial update (only change system_prompt and public)
PATCH /api/v1/agents/my-agent
{
  "system_prompt": "You are an expert.",
  "public": true
}

# Response (all fields, with updates applied)
200 OK
{
  "id": "agent_123",
  "name": "my-agent",
  "model_id": "model_456",          ← unchanged
  "system_prompt": "You are an expert.",  ← updated
  "max_turn_steps": 10,             ← unchanged
  "public": true,                   ← updated
  "config": {...}                   ← unchanged
}
```

### Empty Object Behavior

An empty `{}` payload is valid and results in a 200 response with no changes applied:

```bash
PATCH /api/v1/agents/my-agent
{}

200 OK
{
  "id": "agent_123",
  "name": "my-agent",
  ...  # exactly as before
}
```

### Null Handling

Some fields support explicit nullification via `null`:

```bash
PATCH /api/v1/agents/my-agent
{
  "description": null   # clears description field
}
```

Check field-specific documentation (in `docs/contracts/*.json`) for which fields support null.

### Error Handling

- **400 Bad Request**: Invalid field name or type
- **400 Bad Request**: Field validation fails (e.g., model_id references wrong kind)
- **404 Not Found**: Resource not found
- **409 Conflict**: Concurrent modification or constraint violation

All error responses include a `message` field describing the error.

---

## Resolvers — UUID or Name-Based Lookup

### Specification

Many endpoints accept either UUID or name as the identifier:

**Pattern:**
```
GET /api/v1/{resource}/{id-or-name}
```

The engine attempts to match in this order:
1. UUID prefix match: if `{id-or-name}` is a valid UUID, match by `id`
2. Name exact match: if `{id-or-name}` is not a UUID, match by `name` (case-sensitive)

**Contract:**
- Request: UUID or name string
- Response: Single resource or 404
- Ambiguity: If both UUID and name match, UUID takes precedence
- Exact match: Name matching is exact (case-sensitive, no partial matching)

### Examples

```bash
# By UUID
GET /api/v1/agents/agent_550e8400-e29b-41d4-a716-446655440000
200 OK
{
  "id": "agent_550e8400-e29b-41d4-a716-446655440000",
  "name": "my-agent",
  ...
}

# By name
GET /api/v1/agents/my-agent
200 OK
{
  "id": "agent_550e8400-e29b-41d4-a716-446655440000",
  "name": "my-agent",
  ...
}

# Both exist but UUID takes precedence
GET /api/v1/agents/agent_550e8400
200 OK  # returns by UUID prefix
{
  "id": "agent_550e8400-e29b-41d4-a716-446655440000",
  "name": "something-else",  # name happens to not match 'agent_550e8400'
  ...
}

# Name not found
GET /api/v1/agents/nonexistent
404 Not Found
{"message": "agent not found"}
```

### Supported Endpoints

| Resource | Resolver | UUID | Name |
|---|---|---|---|
| Agent | `/api/v1/agents/{id-or-name}` | ✓ | ✓ |
| Schema | `/api/v1/schemas/{id-or-name}` | ✓ | ✓ |
| Model | `/api/v1/models/{id-or-name}` | ✓ | ✓ |
| Knowledge Base | `/api/v1/knowledge-bases/{id-or-name}` | ✓ | ✓ |
| MCP Server | `/api/v1/mcp-servers/{id-or-name}` | ✓ | ✓ |

---

## Kind-Split Models

### Specification

The `models` table includes a `kind` field to explicitly distinguish between model types:

```
models.kind  varchar(20)  NOT NULL  DEFAULT 'chat'
             CHECK (kind IN ('chat', 'embedding'))
```

**Contract:**
- `kind = 'chat'`: Model accepts system prompts and streams completions (agents)
- `kind = 'embedding'`: Model converts text to vectors (knowledge bases)
- Default: `'chat'` (for backward compatibility)
- Validation: Application layer rejects mismatched kind at request time

### Creating Models

**Chat model:**

```bash
POST /api/v1/models
{
  "name": "my-gpt4",
  "type": "openai_compatible",
  "kind": "chat",                    # optional, defaults to 'chat'
  "model_name": "gpt-4o",
  "api_key": "sk-...",
  "base_url": "https://api.openai.com/v1"
}

201 Created
{
  "id": "model_123",
  "name": "my-gpt4",
  "kind": "chat",
  "type": "openai_compatible",
  "model_name": "gpt-4o",
  ...
}
```

**Embedding model:**

```bash
POST /api/v1/models
{
  "name": "text-embedding-3-small",
  "type": "openai_compatible",
  "kind": "embedding",                # explicit
  "model_name": "text-embedding-3-small",
  "api_key": "sk-...",
  "base_url": "https://api.openai.com/v1",
  "embedding_dim": 1536
}

201 Created
{
  "id": "model_456",
  "name": "text-embedding-3-small",
  "kind": "embedding",
  "embedding_dim": 1536,
  ...
}
```

### Filtering by Kind

```bash
# Chat models only (for agent dropdown)
GET /api/v1/models?kind=chat
200 OK
[
  {"id": "model_123", "name": "my-gpt4", "kind": "chat", ...},
  {"id": "model_789", "name": "gpt-4o-mini", "kind": "chat", ...}
]

# Embedding models only (for knowledge base dropdown)
GET /api/v1/models?kind=embedding
200 OK
[
  {"id": "model_456", "name": "text-embedding-3-small", "kind": "embedding", ...}
]

# All models
GET /api/v1/models
200 OK
[
  {"id": "model_123", "name": "my-gpt4", "kind": "chat", ...},
  {"id": "model_456", "name": "text-embedding-3-small", "kind": "embedding", ...},
  ...
]
```

### Validation Rules

**When assigning a model to an agent:**

```bash
PATCH /api/v1/agents/my-agent
{
  "model_id": "model_456"  # embedding model
}

400 Bad Request
{
  "message": "model_id must reference a chat model, got kind=embedding"
}
```

**When assigning a model to a knowledge base:**

```bash
PATCH /api/v1/knowledge-bases/my-kb
{
  "embedding_model_id": "model_123"  # chat model
}

400 Bad Request
{
  "message": "embedding_model_id must reference an embedding model, got kind=chat"
}
```

### Migration (003_models_kind_split)

Liquibase changeset `003_models_kind_split.yaml` migrates existing deployments:

1. Adds `kind varchar(20) NOT NULL DEFAULT 'chat'` column
2. Backfills: rows with `(config->>'embedding_dim')::int > 0` set to `kind='embedding'`
3. Adds CHECK constraint
4. Safe for pre-Wave-5 deployments — existing embedding models are auto-classified

---

## Authorization and Multi-Tenancy

### Headers

All API requests include tenant context via headers:

| Header | Format | Example | Required |
|---|---|---|---|
| `Authorization` | `Bearer <jwt-token>` | `Bearer eyJ...` | yes |
| `X-Org-Id` | UUID string | `X-Org-Id: org_550e8400-e29b-41d4-a716-446655440000` | Cloud only |
| `X-User-Id` | string | `X-User-Id: user_123` | optional (extracted from JWT `sub` if omitted) |

The `Authorization` header contains either:
- **Session JWT** (from `/api/v1/auth/local-session`) — expires after 24 hours
- **API token** (long-lived, created in Admin Dashboard) — does not expire by default

### Tenant Isolation

All resources are scoped to a tenant ID derived from the JWT `sub` claim:

```bash
# Request as user 'alice@org.com'
GET /api/v1/agents
Authorization: Bearer eyJ...  # JWT with sub='alice@org.com'

200 OK
[
  {"id": "agent_1", "name": "alice-agent", ...},  # alice can see
  ...
]

# Another user 'bob@org.com' sees different agents
GET /api/v1/agents
Authorization: Bearer eyJ...  # JWT with sub='bob@org.com'

200 OK
[
  {"id": "agent_5", "name": "bob-agent", ...},   # bob can see
  ...
]

# Cross-tenant access denied
GET /api/v1/agents/agent_1  # alice's agent
Authorization: Bearer eyJ...  # JWT with sub='bob@org.com'

403 Forbidden
{"message": "agent not found"}  # 403 or 404; never reveals 'other user's resource'
```

---

## Contract Files (JSON Schema)

Per-resource contracts are defined in `docs/contracts/` as JSON Schema:

- `docs/contracts/agent-request.json` — POST/PATCH agent request
- `docs/contracts/agent-response.json` — GET agent response
- `docs/contracts/model-request.json` — POST/PATCH model request
- `docs/contracts/model-response.json` — GET model response
- ... (one per resource type)

Use these schemas for:
- API client code generation
- Request validation in tests
- Documentation of required/optional fields

---

## Examples

### Complete Agent Workflow

```bash
# 1. Create chat model
curl -X POST http://localhost:8443/api/v1/models \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "gpt-4o",
    "type": "openai_compatible",
    "kind": "chat",
    "model_name": "gpt-4o",
    "api_key": "sk-...",
    "base_url": "https://api.openai.com/v1"
  }'

# 2. Create agent
curl -X POST http://localhost:8443/api/v1/agents \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "support-bot",
    "system_prompt": "You are a helpful support agent.",
    "model_id": "gpt-4o"
  }'

# 3. Partial update (only change system_prompt)
curl -X PATCH http://localhost:8443/api/v1/agents/support-bot \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "system_prompt": "You are an expert support agent with access to our knowledge base."
  }'

# 4. Retrieve updated agent (by name)
curl -X GET http://localhost:8443/api/v1/agents/support-bot \
  -H "Authorization: Bearer $TOKEN"
```

### Embedding Model Setup

```bash
# 1. Create embedding model
curl -X POST http://localhost:8443/api/v1/models \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "text-embedding-3-small",
    "type": "openai_compatible",
    "kind": "embedding",
    "model_name": "text-embedding-3-small",
    "api_key": "sk-...",
    "base_url": "https://api.openai.com/v1",
    "embedding_dim": 1536
  }'

# 2. Create knowledge base with embedding model
curl -X POST http://localhost:8443/api/v1/knowledge-bases \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "product-docs",
    "embedding_model_id": "text-embedding-3-small"
  }'

# 3. Attach knowledge base to agent
curl -X PATCH http://localhost:8443/api/v1/agents/support-bot \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "knowledge_base_ids": ["product-docs"]
  }'
```

