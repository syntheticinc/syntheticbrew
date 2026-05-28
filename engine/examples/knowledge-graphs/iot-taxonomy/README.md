# IoT Taxonomy — Example Knowledge Graph Bundle

Generic IoT industry / sensor-family / use-case taxonomy. Drop-in example for
SyntheticBrew Knowledge Graphs (engine 1.3.0+).

Contents:

```
iot-taxonomy/
├── manifest.yaml
├── schemas/
│   ├── industry.schema.json
│   ├── sensor_family.schema.json
│   └── use_case.schema.json
└── entities/
    ├── industries.yaml        (6 entities)
    ├── sensor_families.yaml   (7 entities)
    └── use_cases.yaml         (11 entities, mix of approved/draft/deprecated)
```

## Apply

```bash
brewctl kg apply ./iot-taxonomy \
  --endpoint http://localhost:18082 \
  --token "$BREWCTL_TOKEN"
```

The engine will:

1. Validate the three schemas.
2. Resolve `x-ref` cross-references (use cases → industries, use cases → sensor families).
3. Generate MCP tools `list_industry`, `get_industry`, `list_sensor_family`, `get_sensor_family`, `list_use_case`, `get_use_case` per tenant.
4. Wire them into any agent that has `capabilities: [{type: knowledge_graphs, config: {bundles: [iot-taxonomy-example]}}]`.

## Quickstart agent prompt

Once you have created an agent and bound it to the bundle, paste this as the agent's system prompt to ensure it actually uses the KG tools instead of hallucinating:

```text
You are bound to the iot-taxonomy-example knowledge graph.
You have read-only tools: list_industry, get_industry, list_sensor_family,
get_sensor_family, list_use_case, get_use_case.

MANDATORY workflow on every user question:
  1. Identify which entity_type the question is about.
  2. Use list_/get_ tools — NEVER invent entity ids or attribute values.
  3. If a tool returns 0 results, say so explicitly. Suggest the closest
     existing entities by querying a related type.
  4. Prefer popularity=high industries first when not specified.
  5. Cite the entity id of every recommendation.

Filter values must be ENTITY CODES (lowercase snake_case or short uppercase
industry codes), not display names. Filters is an object, not a JSON-encoded
string. Example: list_use_case(filters={"industry": "PM", "status": "approved"}).
```

See [docs](https://syntheticbrew.ai/docs/concepts/knowledge-graphs/#prompt-engineering-for-kg-grounded-agents)
for why the MANDATORY workflow block is required.

## Try it

After apply, in the admin chat tab ask:

> What approved use cases exist for Property Management?

Expected tool call: `list_use_case(filters={"industry": "PM", "status": "approved"})`.
Expected result: 3 entities (`PM-LEAK-001`, `PM-OCC-002`, `PM-DOOR-003`).

> Tell me about PM-LEAK-001.

Expected tool call: `get_use_case(id="PM-LEAK-001")`. Full payload with the
description appears in the agent's reply.

## License

This example bundle is part of the SyntheticBrew Engine source tree (BSL 1.1).
Free to fork, adapt, and use as a starting point for your own taxonomy.
