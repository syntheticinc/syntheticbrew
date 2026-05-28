// §1.7-1.4 — Knowledge Graphs 1.4.0 query API: batch get, range / IN filter
//             operators, server-side sort with enum-declaration semantics,
//             x-summary-fields projection on list_<entity>_ids.
//
// TC: KG14-API-01..09 (REST-level assertions of the engine 1.4.0 contract).
//
// This is the lower-cost counterpart to the MCP-Playwright suite that lives
// under e2e/knowledge-graphs/mcp-flow/ — the REST surface mirrors the auto
// MCP tool shape, so exercising it here covers the same backend logic with
// faster, more deterministic specs.

import { test, expect, apiFetch } from '../fixtures';

const TYPED_SCHEMA = {
  $schema: 'https://json-schema.org/draft/2020-12/schema',
  $id: 'use_case',
  type: 'object',
  'x-id-field': 'code',
  'x-tool-expose': ['list', 'get', 'list_ids'],
  // Declaring popularity as enum [very_high, high, normal, low] makes the
  // declaration-order sort behaviour testable; declaration order matches the
  // semantic ordering (most popular first).
  'x-summary-fields': ['title', 'industry', 'popularity'],
  required: ['code', 'title', 'industry'],
  additionalProperties: false,
  properties: {
    code: { type: 'string', pattern: '^[A-Z]{2}-[A-Z0-9-]+$', 'x-index': true },
    title: { type: 'string', minLength: 1 },
    industry: { type: 'string', 'x-index': true },
    popularity: {
      type: 'string',
      enum: ['very_high', 'high', 'normal', 'low'],
      'x-index': true,
    },
    score: { type: 'integer', 'x-index': true },
  },
};

const bulkPayload = (entities: Array<Record<string, unknown>>) => ({
  version: '1.0.0',
  schemas: [{ entity_type: 'use_case', schema: TYPED_SCHEMA }],
  entities: [{ entity_type: 'use_case', items: entities }],
});

const newBundle = (prefix: string) =>
  `${prefix}-${Date.now()}-${Math.floor(Math.random() * 10000)}`;

const seed = async (
  request: any,
  adminToken: string,
  entities: Array<Record<string, unknown>>,
  prefix: string,
) => {
  const bundle = newBundle(prefix);
  const res = await apiFetch(request, `/knowledge-graphs/${bundle}/import`, {
    method: 'POST',
    token: adminToken,
    body: bulkPayload(entities),
  });
  expect([200, 201]).toContain(res.status());
  return bundle;
};

const cleanup = async (request: any, adminToken: string, bundle: string) => {
  await apiFetch(request, `/knowledge-graphs/${bundle}`, {
    method: 'DELETE',
    token: adminToken,
  });
};

test.describe('KG 1.4.0 — batch get', () => {
  test('KG14-API-01 batch get preserves input order; missing surfaces in not_found', async ({
    request,
    adminToken,
  }) => {
    const bundle = await seed(
      request,
      adminToken,
      [
        { code: 'PM-A', title: 'A', industry: 'PM', popularity: 'high' },
        { code: 'FB-B', title: 'B', industry: 'FB', popularity: 'normal' },
        { code: 'RT-C', title: 'C', industry: 'RT', popularity: 'low' },
      ],
      'kg14-bg',
    );
    try {
      const res = await apiFetch(
        request,
        `/knowledge-graphs/${bundle}/entities/use_case/batch-get`,
        {
          method: 'POST',
          token: adminToken,
          body: { ids: ['RT-C', 'PM-A', 'NO-SUCH', 'FB-B'] },
        },
      );
      expect(res.status()).toBe(200);
      const body = await res.json();
      expect(body.entities.map((e: any) => e.entity_id)).toEqual(['RT-C', 'PM-A', 'FB-B']);
      expect(body.not_found).toEqual(['NO-SUCH']);
    } finally {
      await cleanup(request, adminToken, bundle);
    }
  });

  test('KG14-API-02 empty ids → 400', async ({ request, adminToken }) => {
    const bundle = await seed(
      request,
      adminToken,
      [{ code: 'PM-A', title: 'A', industry: 'PM', popularity: 'high' }],
      'kg14-bg-empty',
    );
    try {
      const res = await apiFetch(
        request,
        `/knowledge-graphs/${bundle}/entities/use_case/batch-get`,
        { method: 'POST', token: adminToken, body: { ids: [] } },
      );
      expect(res.status()).toBe(400);
      expect(await res.text()).toMatch(/INVALID_INPUT/);
    } finally {
      await cleanup(request, adminToken, bundle);
    }
  });
});

test.describe('KG 1.4.0 — filter operators', () => {
  test('KG14-API-03 [in] multi-value filter on indexed string', async ({
    request,
    adminToken,
  }) => {
    const bundle = await seed(
      request,
      adminToken,
      [
        { code: 'PM-A', title: 'A', industry: 'PM', popularity: 'high' },
        { code: 'FB-B', title: 'B', industry: 'FB', popularity: 'high' },
        { code: 'XX-C', title: 'C', industry: 'XX', popularity: 'high' },
      ],
      'kg14-flt-in',
    );
    try {
      const res = await apiFetch(
        request,
        `/knowledge-graphs/${bundle}/entities/use_case?filter[industry][in]=PM,FB`,
        { token: adminToken },
      );
      expect(res.status()).toBe(200);
      const body = await res.json();
      const codes = body.items.map((i: any) => i.entity_id).sort();
      expect(codes).toEqual(['FB-B', 'PM-A']);
    } finally {
      await cleanup(request, adminToken, bundle);
    }
  });

  test('KG14-API-04 [gte/lte] range on integer field', async ({ request, adminToken }) => {
    const bundle = await seed(
      request,
      adminToken,
      [
        { code: 'PM-A', title: 'A', industry: 'PM', popularity: 'high', score: 60 },
        { code: 'PM-B', title: 'B', industry: 'PM', popularity: 'high', score: 80 },
        { code: 'PM-C', title: 'C', industry: 'PM', popularity: 'high', score: 99 },
      ],
      'kg14-flt-range',
    );
    try {
      const res = await apiFetch(
        request,
        `/knowledge-graphs/${bundle}/entities/use_case?filter[score][gte]=70&filter[score][lte]=95`,
        { token: adminToken },
      );
      expect(res.status()).toBe(200);
      const body = await res.json();
      const codes = body.items.map((i: any) => i.entity_id);
      expect(codes).toEqual(['PM-B']);
    } finally {
      await cleanup(request, adminToken, bundle);
    }
  });

  test('KG14-API-05 range on string field → 400', async ({ request, adminToken }) => {
    const bundle = await seed(
      request,
      adminToken,
      [{ code: 'PM-A', title: 'A', industry: 'PM', popularity: 'high' }],
      'kg14-flt-rstr',
    );
    try {
      const res = await apiFetch(
        request,
        `/knowledge-graphs/${bundle}/entities/use_case?filter[industry][gte]=X`,
        { token: adminToken },
      );
      expect(res.status()).toBe(400);
      expect(await res.text()).toMatch(/range operators not supported/);
    } finally {
      await cleanup(request, adminToken, bundle);
    }
  });
});

test.describe('KG 1.4.0 — sort', () => {
  test('KG14-API-06 enum sort follows declaration order (NOT alphabetical)', async ({
    request,
    adminToken,
  }) => {
    const bundle = await seed(
      request,
      adminToken,
      [
        { code: 'PM-LOW', title: 'L', industry: 'PM', popularity: 'low' },
        { code: 'PM-HI', title: 'H', industry: 'PM', popularity: 'high' },
        { code: 'PM-VH', title: 'V', industry: 'PM', popularity: 'very_high' },
        { code: 'PM-NR', title: 'N', industry: 'PM', popularity: 'normal' },
      ],
      'kg14-srt-enum',
    );
    try {
      const res = await apiFetch(
        request,
        `/knowledge-graphs/${bundle}/entities/use_case?sort=popularity:desc`,
        { token: adminToken },
      );
      expect(res.status()).toBe(200);
      const body = await res.json();
      const codes = body.items.map((i: any) => i.entity_id);
      // Declaration order [very_high, high, normal, low] read "highest first"
      // — desc returns the head of the array first.
      expect(codes).toEqual(['PM-VH', 'PM-HI', 'PM-NR', 'PM-LOW']);
    } finally {
      await cleanup(request, adminToken, bundle);
    }
  });

  test('KG14-API-07 multi-field sort with tiebreaker', async ({ request, adminToken }) => {
    const bundle = await seed(
      request,
      adminToken,
      [
        { code: 'PM-Z', title: 'Z', industry: 'PM', popularity: 'high' },
        { code: 'PM-A', title: 'A', industry: 'PM', popularity: 'high' },
        { code: 'PM-M', title: 'M', industry: 'PM', popularity: 'low' },
      ],
      'kg14-srt-multi',
    );
    try {
      const res = await apiFetch(
        request,
        `/knowledge-graphs/${bundle}/entities/use_case?sort=popularity:desc,code:asc`,
        { token: adminToken },
      );
      expect(res.status()).toBe(200);
      const body = await res.json();
      const codes = body.items.map((i: any) => i.entity_id);
      expect(codes).toEqual(['PM-A', 'PM-Z', 'PM-M']);
    } finally {
      await cleanup(request, adminToken, bundle);
    }
  });

  test('KG14-API-08 sort on non-indexed field → 400', async ({ request, adminToken }) => {
    const bundle = await seed(
      request,
      adminToken,
      [{ code: 'PM-A', title: 'A', industry: 'PM', popularity: 'high' }],
      'kg14-srt-noidx',
    );
    try {
      const res = await apiFetch(
        request,
        `/knowledge-graphs/${bundle}/entities/use_case?sort=title:asc`,
        { token: adminToken },
      );
      expect(res.status()).toBe(400);
      expect(await res.text()).toMatch(/indexed/i);
    } finally {
      await cleanup(request, adminToken, bundle);
    }
  });
});

test.describe('KG 1.4.0 — x-summary-fields', () => {
  test('KG14-API-09 schema exposes x-summary-fields annotation to admin SPA', async ({
    request,
    adminToken,
  }) => {
    const bundle = await seed(
      request,
      adminToken,
      [{ code: 'PM-A', title: 'A', industry: 'PM', popularity: 'high' }],
      'kg14-sf',
    );
    try {
      const res = await apiFetch(
        request,
        `/knowledge-graphs/${bundle}/schemas/use_case`,
        { token: adminToken },
      );
      expect(res.status()).toBe(200);
      const body = await res.json();
      // Schema doc round-trip — admin SPA reads the annotation off this payload
      // via the kgSummaryFields() helper added in 1.4.0.
      expect(body.schema_json['x-summary-fields']).toEqual([
        'title',
        'industry',
        'popularity',
      ]);
    } finally {
      await cleanup(request, adminToken, bundle);
    }
  });
});
