# E-commerce Catalog — Example Knowledge Graph Bundle

Generic e-commerce product taxonomy: categories, brands, product
attributes. Drop-in example for SyntheticBrew Knowledge Graphs
(engine 1.3.0+).

This bundle is intentionally a **neutral generic catalog**. It is not
modeled on any specific customer's real catalog — it exists purely to
show what a real Knowledge Graph bundle looks like end-to-end.

Contents:

```
ecommerce-catalog/
├── manifest.yaml
├── schemas/
│   ├── category.schema.json
│   ├── brand.schema.json
│   └── product_attribute.schema.json
└── entities/
    ├── categories.yaml          (8 entities — 5 primary, 3 secondary)
    ├── brands.yaml              (7 brands across budget/mid/premium/luxury)
    └── product_attributes.yaml  (8 filter / facet attributes)
```

## Apply

```bash
brewctl kg apply ./ecommerce-catalog \
  --engine http://localhost:18082 \
  --token "$BREWCTL_TOKEN"
```

The engine will:

1. Validate the three schemas.
2. Resolve `x-ref` cross-references (brands → categories, product_attributes → categories).
3. Generate MCP tools `list_category`, `get_category`, `list_brand`, `get_brand`, `list_product_attribute`, `get_product_attribute` per tenant.
4. Wire them into any agent that has `capabilities: [{type: knowledge_graphs, config: {bundles: [ecommerce-catalog-example]}}]`.

## Recommended agent system prompt

Once you have created an agent and bound it to the bundle, paste this as
the agent's system prompt to ensure it actually uses the KG tools
instead of hallucinating:

```text
You are bound to the ecommerce-catalog-example knowledge graph.
You have read-only tools: list_category, get_category, list_brand,
get_brand, list_product_attribute, get_product_attribute.

MANDATORY workflow on every user question:
  1. Identify which entity_type the question is about (categories vs
     brands vs attributes).
  2. Use list_/get_ tools — NEVER invent entity codes or attribute values.
  3. If a tool returns 0 results, say so explicitly. Suggest the closest
     existing entities by querying a related type.
  4. Prefer popularity=high categories first when not specified.
  5. Cite the entity code of every recommendation.

Filter values must be ENTITY CODES (lowercase snake_case or kebab-case
short identifiers), not display names. Filters is an object, not a
JSON-encoded string. Example:
  list_brand(filters={"category": "footwear", "tier": "premium"}).
```

See [docs](https://syntheticbrew.ai/docs/concepts/knowledge-graphs/#prompt-engineering-for-kg-grounded-agents)
for why the MANDATORY workflow block is required.

## Try it

After apply, in the admin chat tab ask:

> What premium footwear brands do we carry?

Expected tool call: `list_brand(filters={"category": "footwear", "tier": "premium"})`.
Expected result: one entity (`stride-co` is `mid`, so the agent should
also mention that there are no `premium` footwear brands in the catalog
and suggest the closest match).

> Tell me about north-aurora.

Expected tool call: `get_brand(id="north-aurora")`. Full payload with
the headquarters and category appears in the agent's reply.

> Which attributes filter the apparel category?

Expected tool call: `list_product_attribute(filters={"category": "apparel"})`.
Returns: material / fit / season.

## License

This example bundle is part of the SyntheticBrew Engine source tree (BSL 1.1).
Free to fork, adapt, and use as a starting point for your own taxonomy.
