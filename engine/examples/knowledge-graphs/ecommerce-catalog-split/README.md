# E-commerce Catalog (split layout) — Example Knowledge Graph Bundle

Same content as the sibling `ecommerce-catalog/` example, organised with
the **engine 1.4.0 + brewctl 0.4.0** `entities_path` split-layout
authoring pattern instead of one big `entities_file` per entity_type.

## When to use this layout

- Catalogs with ≥100 entities per type where a single YAML file becomes
  hard to review in PRs.
- Per-axis authoring (split by tier / category / industry / shard).
- Per-entity PR approval workflows (each entity in its own file).

For smaller catalogs (<100 entities/type) the canonical single-file
layout in `ecommerce-catalog/` is simpler and just as correct. Pick the
layout that fits your authoring workflow; the engine sees the same
flattened payload either way.

## Contents

```
ecommerce-catalog-split/
├── manifest.yaml
├── schemas/
│   ├── category.schema.json
│   ├── brand.schema.json                ← x-summary-fields enabled
│   └── product_attribute.schema.json
└── entities/
    ├── categories.yaml                  ← single-file (still fine at 8 entries)
    ├── brand/                           ← split by tier
    │   ├── premium.yaml                 (array of 2 entities)
    │   ├── mid.yaml                     (array of 4 entities)
    │   └── budget-basics.yaml           (one entity, single-document form)
    └── product_attribute/               ← split by parent category
        ├── apparel.yaml                 (array of 3 entities)
        ├── footwear.yaml                (array of 2 entities)
        └── other.yaml                   (array of 3 entities)
```

Each file inside an `entities_path` directory can be **either**:

- An array of entity objects (e.g. `brand/mid.yaml`), or
- A single entity object (e.g. `brand/budget-basics.yaml`).

brewctl globs `*.yaml`/`*.yml` flat (no recursion), parses each, and
merges into one atomic apply. Duplicate entity_id across files is
detected at load time with a clear error naming both files.

## Apply

```bash
brewctl kg apply ./ecommerce-catalog-split \
  --engine http://localhost:18082 \
  --token "$BREWCTL_TOKEN"
```

The engine receives the same flattened payload as the single-file example:
8 categories + 7 brands + 8 product attributes. Tool generation, capability
binding, query API behaviour — all identical to the canonical layout.

## Mutual exclusion

Per the manifest schema you may declare **either** `entities_file` **or**
`entities_path` on each entity_type, never both. Different entity_types
in the same manifest can mix layouts — `category` here uses the
single-file form while `brand` and `product_attribute` use split.

## Pull behaviour

`brewctl kg pull` always emits the canonical single-file layout, even for
bundles authored with `entities_path`. The roundtrip is lossy on pull
by design — the engine does not record which file each entity came from.
Treat pull as a backup / inspection tool, not a roundtrip authoring tool.

If you need to reconstruct a split layout after a pull, write a small
local script that re-splits entities by your chosen axis (industry,
category, tier, etc.). A future release may add a manifest option to
make pull preserve a deterministic split layout if customer demand emerges.

## See also

- `../ecommerce-catalog/` — canonical single-file layout, simpler for newcomers
- [Bundles & layouts guide](https://syntheticbrew.ai/docs/concepts/knowledge-graphs-bundles/)
- [Migration 1.3 → 1.4](https://syntheticbrew.ai/docs/getting-started/migration-1.3-to-1.4/)

## License

This example bundle is part of the SyntheticBrew Engine source tree (BSL 1.1).
