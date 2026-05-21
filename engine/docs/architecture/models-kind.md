# Chat vs Embedding Model Split (`models.kind`)

## Why the split exists

SyntheticBrew agents require a **chat** model (one that accepts a system prompt and
generates streamed completions). Knowledge bases require an **embedding** model
(one that converts text into dense vectors for similarity search).

The two model types are incompatible:
- Sending a chat prompt to an embedding endpoint returns an error.
- Using an embedding model as an agent's LLM produces no conversational output.

Before Wave 5 (CLOUD-ARCH-UNIFY-2026-04-21) the engine relied on a heuristic:
a model with a positive `config.embedding_dim` was considered an embedding model
and was blocked from agent assignment. That heuristic depended on the operator
filling in `embedding_dim` correctly. The `kind` column replaces the heuristic
with an explicit, validated discriminator stored directly on the row.

## The `kind` column

```
models.kind  varchar(20)  NOT NULL  DEFAULT 'chat'
             CHECK (kind IN ('chat', 'embedding'))
```

- Application layer is the **primary** enforcement point (400 on mismatch).
- The `CHECK` constraint is a DB-side backstop only.
- `kind` is independent of `models.type` (which remains the provider enum:
  `ollama`, `openai_compatible`, `anthropic`, `azure_openai`).

## API

### Creating a chat model

```bash
curl -X POST http://localhost:9555/api/v1/models \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name":       "my-gpt4",
    "type":       "openai_compatible",
    "model_name": "gpt-4o",
    "api_key":    "sk-...",
    "base_url":   "https://api.openai.com/v1"
  }'
```

`kind` defaults to `"chat"` when omitted. The response includes `"kind": "chat"`.

### Creating an embedding model

```bash
curl -X POST http://localhost:9555/api/v1/models \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name":          "text-embedding-3-small",
    "type":          "openai_compatible",
    "kind":          "embedding",
    "model_name":    "text-embedding-3-small",
    "api_key":       "sk-...",
    "base_url":      "https://api.openai.com/v1",
    "embedding_dim": 1536
  }'
```

### Filtering by kind

```bash
# Only embedding models (for KB dropdown):
GET /api/v1/models?kind=embedding

# Only chat models (for agent dropdown):
GET /api/v1/models?kind=chat

# All models:
GET /api/v1/models
```

## Validation rules

| Operation | Field | Required kind | Error on mismatch |
|-----------|-------|--------------|-------------------|
| POST/PUT/PATCH `/agents` | `model_id` | `chat` | 400 `model_id must reference a chat model, got kind=embedding` |
| POST/PUT/PATCH `/knowledge-bases` | `embedding_model_id` | `embedding` | 400 `embedding_model_id must reference an embedding model, got kind=chat` |

## Migration impact (003_models_kind_split)

Migration `003_models_kind_split` (Liquibase changeset registered in
`db.changelog-master.yaml`):

1. Adds `kind varchar(20) NOT NULL DEFAULT 'chat'` to `models`.
2. Backfills existing rows: rows with `(config->>'embedding_dim')::int > 0`
   are set to `kind = 'embedding'`; all others remain `'chat'`.
3. Adds `CHECK (kind IN ('chat', 'embedding'))`.

The backfill is safe for all pre-Wave-5 installs: any embedding model
configured via `embedding_dim` is automatically classified as `'embedding'`.
Operators do not need to re-create models.

## BYOK model

SyntheticBrew Cloud does **not** pre-seed embedding models. Operators bring their
own provider and API key — the same as chat models. Typical choices:

- **OpenAI** — `text-embedding-3-small` (1536 dims), `text-embedding-3-large` (3072 dims)
- **Ollama** — `nomic-embed-text` (768 dims), `mxbai-embed-large` (1024 dims)
- **Azure OpenAI** — any Azure-hosted embedding deployment

Use the Admin Dashboard (Models page) or the API to register the model with
`"kind": "embedding"` and set `embedding_dim` to the correct dimension for
your chosen model.
