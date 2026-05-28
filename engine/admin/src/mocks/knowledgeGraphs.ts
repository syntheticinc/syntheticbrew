// Mock data for Knowledge Graphs page (prototype mode).
//
// Backend API is not deployed yet — these mocks let the UI render and be
// exercised in prototype mode. Real API calls will replace them once the
// engine exposes /api/v1/knowledge-graphs.
//
// Naming is intentionally generic ("ecommerce-catalog") to avoid
// client/partner names. Schemas (category, brand, product_attribute) are
// representative of a small taxonomy bundle.

import type { KGBundle, KGEntitySchema, KGEntity } from '../types';

// ─── Bundles ────────────────────────────────────────────────────────────────

export const MOCK_KG_BUNDLES: KGBundle[] = [
  {
    bundle_name: 'ecommerce-catalog',
    version: '0.3.1',
    manifest: {
      entity_types: ['category', 'brand', 'product_attribute'],
      counts: { category: 8, brand: 7, product_attribute: 8 },
      schema_hashes: {
        category: 'sha256:c4d1a2b9',
        brand: 'sha256:8b22f0e7',
        product_attribute: 'sha256:71e9adcb',
      },
    },
    created_at: '2026-05-01T09:30:00Z',
    updated_at: '2026-05-22T14:12:00Z',
  },
];

// ─── Entity Schemas ─────────────────────────────────────────────────────────

export const MOCK_KG_SCHEMAS: Record<string, KGEntitySchema[]> = {
  'ecommerce-catalog': [
    {
      bundle_name: 'ecommerce-catalog',
      entity_type: 'category',
      id_field: 'code',
      schema_hash: 'sha256:c4d1a2b9',
      expose_tools: ['list_category', 'get_category'],
      tool_description: 'Catalog categories — top-level browse groupings',
      schema_json: {
        type: 'object',
        required: ['code', 'name', 'tier'],
        properties: {
          code: { type: 'string', 'x-index': true },
          name: { type: 'string', 'x-index': true },
          tier: { type: 'string', 'x-index': true, enum: ['primary', 'secondary'] },
          parent_code: { type: 'string', 'x-cross-ref': 'category' },
          description: { type: 'string' },
        },
      },
    },
    {
      bundle_name: 'ecommerce-catalog',
      entity_type: 'brand',
      id_field: 'code',
      schema_hash: 'sha256:8b22f0e7',
      expose_tools: ['list_brand', 'get_brand'],
      tool_description: 'Brands carried in the catalog, grouped by category',
      schema_json: {
        type: 'object',
        required: ['code', 'name', 'tier'],
        properties: {
          code: { type: 'string', 'x-index': true },
          name: { type: 'string', 'x-index': true },
          tier: { type: 'string', 'x-index': true, enum: ['budget', 'mid', 'premium', 'luxury'] },
          category: { type: 'string', 'x-cross-ref': 'category', 'x-index': true },
          headquarters: { type: 'string' },
        },
      },
    },
    {
      bundle_name: 'ecommerce-catalog',
      entity_type: 'product_attribute',
      id_field: 'code',
      schema_hash: 'sha256:71e9adcb',
      expose_tools: ['list_product_attribute', 'get_product_attribute'],
      tool_description: 'Filter / facet attributes available for a category',
      schema_json: {
        type: 'object',
        required: ['code', 'name'],
        properties: {
          code: { type: 'string', 'x-index': true },
          name: { type: 'string', 'x-index': true },
          category: { type: 'string', 'x-cross-ref': 'category', 'x-index': true },
          value_type: { type: 'string', 'x-index': true, enum: ['enum', 'boolean', 'integer', 'range'] },
          allowed_values: { type: 'array', items: { type: 'string' } },
        },
      },
    },
  ],
};

// ─── Entities ───────────────────────────────────────────────────────────────

const categories: Array<[string, string, string]> = [
  ['apparel', 'Apparel', 'primary'],
  ['footwear', 'Footwear', 'primary'],
  ['home_goods', 'Home Goods', 'primary'],
  ['books', 'Books', 'primary'],
  ['outdoor', 'Outdoor & Sports', 'primary'],
  ['tops', 'Tops', 'secondary'],
  ['bottoms', 'Bottoms', 'secondary'],
  ['outerwear', 'Outerwear', 'secondary'],
];

const brands: Array<[string, string, string, string, string]> = [
  ['north-aurora', 'North Aurora', 'premium', 'outerwear', 'Vancouver, Canada'],
  ['harborline', 'Harborline Apparel', 'mid', 'apparel', 'Boston, USA'],
  ['stride-co', 'Stride Co.', 'mid', 'footwear', 'Portland, USA'],
  ['alpenfeld', 'Alpenfeld Hiking', 'premium', 'outdoor', 'Munich, Germany'],
  ['terracotta-press', 'Terracotta Press', 'mid', 'books', ''],
  ['oakwood-home', 'Oakwood Home', 'mid', 'home_goods', 'Stockholm, Sweden'],
  ['budget-basics', 'Budget Basics', 'budget', 'apparel', ''],
];

const productAttributes: Array<[string, string, string, string]> = [
  ['material', 'Material', 'apparel', 'enum'],
  ['fit', 'Fit', 'apparel', 'enum'],
  ['season', 'Season', 'apparel', 'enum'],
  ['shoe_size_eu', 'Size (EU)', 'footwear', 'integer'],
  ['arch_support', 'Arch support', 'footwear', 'enum'],
  ['waterproof', 'Waterproof', 'outdoor', 'boolean'],
  ['pages', 'Page count', 'books', 'range'],
  ['assembly_required', 'Assembly required', 'home_goods', 'boolean'],
];

function entitiesForBundle(): Record<string, KGEntity[]> {
  const bundle = 'ecommerce-catalog';
  return {
    category: categories.map(([code, name, tier]) => ({
      bundle_name: bundle,
      entity_type: 'category',
      entity_id: code,
      schema_hash: 'sha256:c4d1a2b9',
      data: { code, name, tier, description: `${name} category` },
    })),
    brand: brands.map(([code, name, tier, category, headquarters]) => ({
      bundle_name: bundle,
      entity_type: 'brand',
      entity_id: code,
      schema_hash: 'sha256:8b22f0e7',
      data: { code, name, tier, category, headquarters },
    })),
    product_attribute: productAttributes.map(([code, name, category, value_type]) => ({
      bundle_name: bundle,
      entity_type: 'product_attribute',
      entity_id: code,
      schema_hash: 'sha256:71e9adcb',
      data: { code, name, category, value_type },
    })),
  };
}

export const MOCK_KG_ENTITIES: Record<string, Record<string, KGEntity[]>> = {
  'ecommerce-catalog': entitiesForBundle(),
};
