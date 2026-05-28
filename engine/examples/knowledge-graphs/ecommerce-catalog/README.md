# E-commerce Catalog — Example Knowledge Graph Bundle

Generic e-commerce product taxonomy: categories, brands, product
attributes. Drop-in example for SyntheticBrew Knowledge Graphs
(engine 1.3.0+; the brand schema demonstrates the **1.4.0** query-API
annotations).

This bundle is intentionally a **neutral generic catalog**. It is not
modeled on any specific customer's real catalog — it exists purely to
show what a real Knowledge Graph bundle looks like end-to-end.

Contents:

```
ecommerce-catalog/
├── manifest.yaml
├── schemas/
│   ├── category.schema.json
│   ├── brand.schema.json           ← x-summary-fields + x-tool-expose [list, get, list_ids]
│   └── product_attribute.schema.json
└── entities/
    ├── categories.yaml          (8 entities — 5 primary, 3 secondary)
    ├── brands.yaml              (7 brands across budget/mid/premium/luxury)
    └── product_attributes.yaml  (8 filter / facet attributes)
```

For an example of the **1.4.0 split layout** (`entities_path` instead of
`entities_file`), see the sibling `ecommerce-catalog-split/` directory —
same data, different on-disk organization.

## Apply

```bash
brewctl kg apply ./ecommerce-catalog \
  --engine http://localhost:18082 \
  --token "$BREWCTL_TOKEN"
```

The engine will:

1. Validate the three schemas (including the `brand` schema's
   `x-summary-fields` annotation if present).
2. Resolve `x-ref` cross-references (brands → categories, product_attributes → categories).
3. Generate MCP tools per tenant:
   - `list_category`, `get_category` (single-id REST + batch tool)
   - `list_brand`, `get_brand`, **`list_brand_ids`** (1.4.0 — `brand` schema declares `list_ids` exposure and `x-summary-fields`, so this tool returns `{items: [{code, name, tier, category}], total}` instead of bare ids)
   - `list_product_attribute`, `get_product_attribute`
4. Wire them into any agent that has `capabilities: [{type: knowledge_graphs, config: {bundles: [ecommerce-catalog-example]}}]`.

## Recommended agent system prompt

Once you have created an agent and bound it to the bundle, paste this as
the agent's system prompt to ensure it actually uses the KG tools
instead of hallucinating:

```text
You are bound to the ecommerce-catalog-example knowledge graph.
You have read-only tools:
  list_category, get_category
  list_brand, get_brand, list_brand_ids
  list_product_attribute, get_product_attribute

MANDATORY workflow on every user question:
  1. Identify which entity_type the question is about (categories vs
     brands vs attributes).
  2. For brand discovery prefer the cheap preview pass:
     list_brand_ids(filters={...}) → returns {items, total} with code,
     name, tier, category. Pick the brands worth a full payload, then
     get_brand(ids=["code-a","code-b"]) for those only.
  3. Use list_/get_ tools — NEVER invent entity codes or attribute values.
  4. If a tool returns 0 results, say so explicitly. Suggest the closest
     existing entities by querying a related type.
  5. Cite the entity code of every recommendation.

Filter values must be ENTITY CODES (lowercase snake_case or kebab-case
short identifiers), not display names. Filters is an object, not a
JSON-encoded string.

Examples:
  list_brand_ids(filters={"category": "footwear", "tier": "premium"})
  get_brand(ids=["north-aurora"])                                    ← 1.4.0 array form
  list_brand_ids(filters={"tier": {"in": ["premium", "luxury"]}},
                 sort=[{"field": "tier", "order": "desc"}], limit=5) ← 1.4.0 IN + sort
```

Note: enum field `tier` in the brand schema is declared in the order
`[luxury, premium, mid, budget]`. Sorting `tier: desc` therefore returns
`luxury` first, NOT alphabetical — by design.

See [docs](https://syntheticbrew.ai/docs/concepts/knowledge-graphs/#prompt-engineering-for-kg-grounded-agents)
for why the MANDATORY workflow block is required.

## Try it (1.4.0 query patterns)

After apply, in the admin chat tab ask:

> What premium footwear brands do we carry?

Expected tool call: `list_brand_ids(filters={"category": "footwear", "tier": "premium"})`.
Expected result: `{items: [...], total: N}` with code + name + tier + category.

> Tell me about north-aurora and stride-co.

Expected tool call: `get_brand(ids=["north-aurora", "stride-co"])`.
Expected result: `{entities: [...two payloads...], not_found: []}` in input order.

> Which attributes filter the apparel category?

Expected tool call: `list_product_attribute(filters={"category": "apparel"})`.
Returns: material / fit / season.

## License

This example bundle is part of the SyntheticBrew Engine source tree (BSL 1.1).
Free to fork, adapt, and use as a starting point for your own taxonomy.
