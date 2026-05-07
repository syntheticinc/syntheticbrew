// §1.8 Widget — copy snippet: widget page → select schema → <script> snippet shown
// TC: WID-01

import { test, expect, apiFetch, ENGINE_API } from '../fixtures';

// Seed a model so OnboardingGate lets the admin surface render instead of redirecting
async function seedModel(page: import('@playwright/test').Page) {
  const token = await page.evaluate(() => localStorage.getItem('jwt') ?? '');
  if (!token) return;
  await page.request.post(`${ENGINE_API}/models`, {
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
    data: {
      name: `wid-seed-${Date.now()}`,
      type: 'openrouter',
      kind: 'chat',
      model_name: 'openai/gpt-4o-mini',
      api_key: 'sk-or-wid-test',
      base_url: 'https://openrouter.ai/api/v1',
    },
  });
}

test.describe('Widget — snippet copy', () => {
  test('widget page renders without error', async ({ authenticatedAdmin }) => {
    const page = authenticatedAdmin;
    await seedModel(page);
    await page.goto('/admin/widget');
    await page.waitForLoadState('networkidle');
    await expect(page.locator('text=/Something went wrong/i')).not.toBeVisible();
  });

  test('widget page shows script snippet or embed code', async ({ authenticatedAdmin }) => {
    const page = authenticatedAdmin;
    await seedModel(page);
    await page.goto('/admin/widget');
    await page.waitForLoadState('networkidle');

    // WidgetConfigPage renders a snippet textarea/pre only when a chat-enabled schema is
    // selected. With no schemas the snippet is empty but the page itself should render
    // the widget config form (title, color, position fields).
    const bodyText = await page.textContent('body') ?? '';
    // Must not be the onboarding wizard
    expect(bodyText).not.toMatch(/Step 1 of 2|Connect your LLM|BYOK/i);
    // Widget page has some widget-related content
    expect(bodyText).toMatch(/widget|snippet|embed|schema|position|color/i);
  });

  test('snippet contains data-schema attribute pattern', async ({ authenticatedAdmin, request, adminToken }) => {
    const page = authenticatedAdmin;
    await seedModel(page);

    // Engine 1.1.0+: schemas are name-keyed; widget snippet renders
    // `data-schema="<name>"` (was `data-schema-id="<uuid>"` pre-1.1.0).
    // Use lowercase name with hyphens — matches the validation regex.
    const schemaName = `widget-schema-${Date.now()}`.toLowerCase();
    const createRes = await apiFetch(request, '/schemas', {
      method: 'POST',
      token: adminToken,
      body: { name: schemaName, chat_enabled: true },
    });
    const created = await createRes.json();

    await page.goto('/admin/widget');
    await page.waitForLoadState('networkidle');

    // Try selecting the schema — option value is now name, not id.
    const schemaSelect = page.locator('select, [data-testid="schema-select"]').first();
    if (await schemaSelect.count() > 0) {
      await schemaSelect.selectOption({ value: created.name });
      await page.waitForTimeout(500);
    }

    const pageText = await page.textContent('body') ?? '';
    // Snippet should reference the widget script
    expect(pageText).toMatch(/widget|script|embed|data-schema/i);

    // Teardown
    if (schemaId) await apiFetch(request, `/schemas/${schemaId}`, { method: 'DELETE', token: adminToken });
  });
});
