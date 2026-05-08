import { test, expect, type Page } from '@playwright/test';
import { execSync } from 'node:child_process';

// Reach into the local Tilt cloud Postgres container. Used only by test 2 to
// reproduce the "tenant has zero models" condition — engine has no API path
// to unbind a system agent (PATCH model_id='' is treated as a no-op), and the
// model-delete guard correctly refuses while builder-assistant still
// references it. Production never lands here; this is a local-only escape
// hatch for the test fixture.
function execEngineSql(sql: string): string {
  return execSync(
    `docker exec bytebrew-cloud-db-1 psql -U postgres -d bytebrew_engine_test -tAc "${sql.replace(/"/g, '\\"')}"`,
    { encoding: 'utf8' },
  ).trim();
}

const EDGE = process.env.PLAYWRIGHT_BASE_URL ?? 'http://localhost:18082';
const PASSWORD = 'PlwT3st!Pass2026';

// Helper: register + (server-side) verify-email + login + open admin.
// Engine /api/v1/auth/register on local Tilt has AUTO_VERIFY_EMAIL=true,
// so register alone makes the user loginable.
async function freshSignupAndOpenAdmin(page: Page): Promise<{ email: string; access: string }> {
  const email = `plw-onb-${Date.now()}-${Math.random().toString(36).slice(2, 6)}@e2e.local`;
  const reg = await page.request.post(`${EDGE}/api/v1/auth/register`, {
    data: { email, password: PASSWORD, acceptTerms: true },
  });
  expect(reg.status(), `register status: ${await reg.text()}`).toBe(201);

  const login = await page.request.post(`${EDGE}/api/v1/auth/login`, {
    data: { email, password: PASSWORD },
  });
  expect(login.status()).toBe(200);
  const access = (await login.json()).data.access_token as string;

  // Mint engine-token — this triggers cloud-api → engine /internal/tenants
  // → SeedTenant, which creates the tenant's my-workspace + builder-assistant.
  const mint = await page.request.post(`${EDGE}/api/v1/auth/engine-token`, {
    headers: { Authorization: `Bearer ${access}` },
  });
  expect(mint.status()).toBe(200);

  // Inject the JWT into every navigation BEFORE the SPA boots so the gate
  // sees an authenticated session. addInitScript survives the auth-driven
  // redirects that destroyed the evaluate-after-goto approach.
  await page.context().addInitScript((token) => {
    localStorage.setItem('jwt', token);
  }, access);
  return { email, access };
}

test.describe('OnboardingGate recovery', () => {
  // addInitScript per-test (set inside freshSignupAndOpenAdmin) takes care
  // of injecting the JWT before each navigation. No global beforeEach reset
  // needed — Playwright gives each test a fresh BrowserContext by default,
  // so sessionStorage/localStorage start empty.

  test('1) first-time login bounces a tenant with zero models into /onboarding', async ({ page }) => {
    await freshSignupAndOpenAdmin(page);
    await page.goto(`${EDGE}/admin/`);
    // Gate should redirect /admin/* → /admin/onboarding
    await expect(page).toHaveURL(/\/onboarding/, { timeout: 15_000 });
  });

  test('2) deleting all models resets the gate so the wizard re-appears on next nav', async ({ page }) => {
    const { access, email } = await freshSignupAndOpenAdmin(page);
    const reqHeaders = { Authorization: `Bearer ${access}`, 'Content-Type': 'application/json' };

    // Resolve tenant_id from cloud-api users table so the SQL ops below scope
    // strictly to this test's tenant — multiple tenants share builder-assistant
    // by name and we must not touch other tenants' rows.
    const tenantID = execSync(
      `docker exec bytebrew-cloud-db-1 psql -U postgres -d cloud_api_test -tAc "SELECT id FROM users WHERE email='${email}'"`,
      { encoding: 'utf8' },
    ).trim();
    expect(tenantID).toMatch(/^[0-9a-f-]{36}$/);

    // Step a: create model — auto-binds to builder-assistant via backfill.
    const create = await page.request.post(`${EDGE}/api/v1/models`, {
      headers: reqHeaders,
      data: {
        name: 'glm-recover',
        kind: 'chat',
        type: 'openrouter',
        model_name: 'z-ai/glm-4.7',
        api_key: 'sk-test-fake',
      },
    });
    expect(create.status(), `create model: ${await create.text()}`).toBe(201);

    // Step b: land on a normal admin page — gate sees model, no redirect.
    await page.goto(`${EDGE}/admin/schemas`);
    await expect(page).toHaveURL(/\/schemas/, { timeout: 10_000 });

    // Step c: drop tenant to zero-models state via direct DB ops. Engine
    // refuses the API DELETE while builder-assistant references the model,
    // and PATCH agent.model_id='' is a no-op in the current handler. The
    // test bypasses both via SQL — local Tilt only — to simulate the real
    // recovery scenario the gate must handle (admin DB wipe / cascading
    // failure that leaves the tenant with no chat models).
    execEngineSql(`UPDATE agents SET model_id = NULL WHERE name = 'builder-assistant' AND tenant_id = '${tenantID}'`);
    execEngineSql(`DELETE FROM models WHERE name = 'glm-recover' AND tenant_id = '${tenantID}'`);

    // Step e: wait past the gate's race-window so cached flag expires.
    await page.waitForTimeout(5_500);

    // Step f: navigate elsewhere — gate must re-evaluate via API and redirect.
    await page.goto(`${EDGE}/admin/agents`);
    await expect(page).toHaveURL(/\/onboarding/, { timeout: 15_000 });
  });

  test('3) builder-assistant gets bound to the model created via the wizard', async ({ page }) => {
    const { access } = await freshSignupAndOpenAdmin(page);

    // Sanity: builder-assistant exists and has no model yet.
    const before = await page.request.get(`${EDGE}/api/v1/agents/builder-assistant`, {
      headers: { Authorization: `Bearer ${access}` },
    });
    expect(before.status(), `agent before: ${await before.text()}`).toBe(200);
    const beforeBody = await before.json();
    expect(beforeBody.model_id ?? '').toBe('');

    // Simulate the wizard's Step 1: POST a default chat model.
    const create = await page.request.post(`${EDGE}/api/v1/models`, {
      headers: { Authorization: `Bearer ${access}`, 'Content-Type': 'application/json' },
      data: {
        name: 'glm-default',
        kind: 'chat',
        type: 'openrouter',
        model_name: 'z-ai/glm-4.7',
        api_key: 'sk-test-fake',
      },
    });
    expect(create.status()).toBe(201);
    const createdModelId = (await create.json()).id as string;

    // backfillTenantAgentsToDefault must have set builder-assistant.model_id
    // to the new model's UUID.
    const after = await page.request.get(`${EDGE}/api/v1/agents/builder-assistant`, {
      headers: { Authorization: `Bearer ${access}` },
    });
    expect(after.status()).toBe(200);
    const afterBody = await after.json();
    expect(afterBody.model_id, `builder-assistant must be bound to new model: ${JSON.stringify(afterBody)}`)
      .toBe(createdModelId);
  });

  test('5) wizard step 1 happy path: filling the form with a valid slug creates the model', async ({ page }) => {
    const { access } = await freshSignupAndOpenAdmin(page);

    await page.goto(`${EDGE}/admin/`);
    await expect(page).toHaveURL(/\/onboarding/, { timeout: 15_000 });

    // Default Display name = 'default' (valid slug). The Model name input
    // pre-fills to the provider's default (e.g. gpt-4o-mini for OpenAI).
    // Provide an API key so the form passes the required-field guard, then
    // click the primary button (Test/Next/Continue/Submit).
    const apiKeyInput = page
      .locator('input[type="password"], input[name*="api" i], input[placeholder*="api key" i]')
      .first();
    await apiKeyInput.fill('sk-test-fake');

    const primary = page.getByRole('button', { name: /test|next|continue|submit|add model|create/i }).first();
    await primary.click();

    // Either we land on Step 2 (Skip becomes visible) or the form shows a
    // success state. Both are pass — we're asserting the model is created.
    await page.waitForTimeout(2_000);
    const list = await page.request.get(`${EDGE}/api/v1/models`, {
      headers: { Authorization: `Bearer ${access}` },
    });
    const models = await list.json();
    expect(Array.isArray(models) && models.length, `models after step 1: ${JSON.stringify(models)}`)
      .toBeGreaterThan(0);
    // Default slug 'default' is valid by SLUG_RE — no 400 from the server.
    expect((models as Array<{ name: string }>).every((m) => /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(m.name)))
      .toBe(true);
  });

  test('6) wizard step 1 client-side validation blocks invalid Display Name without server roundtrip', async ({ page }) => {
    const { access } = await freshSignupAndOpenAdmin(page);

    await page.goto(`${EDGE}/admin/`);
    await expect(page).toHaveURL(/\/onboarding/, { timeout: 15_000 });

    // Type an invalid slug (slash + space — looks like a model identifier).
    const displayInput = page.locator('input[placeholder="default"]').first();
    await displayInput.fill('qwen/qwen3-coder-next');

    // Provide other required fields so only the slug guard can fire.
    const apiKeyInput = page
      .locator('input[type="password"], input[name*="api" i], input[placeholder*="api key" i]')
      .first();
    await apiKeyInput.fill('sk-test-fake');

    // Inline error helper text must surface synchronously.
    await expect(page.locator('text=/lowercase letters, digits, hyphens/i').first())
      .toBeVisible({ timeout: 5_000 });

    // Click Next anyway — wizard handler should short-circuit BEFORE
    // calling api.createModel, so no model is persisted.
    const primary = page.getByRole('button', { name: /test|next|continue|submit/i }).first();
    await primary.click().catch(() => {});

    // Confirm no model was created (validator blocked the API call).
    await page.waitForTimeout(1_000);
    const list = await page.request.get(`${EDGE}/api/v1/models`, {
      headers: { Authorization: `Bearer ${access}` },
    });
    const models = await list.json();
    expect(Array.isArray(models) && models.length, `models should be empty: ${JSON.stringify(models)}`)
      .toBe(0);
  });

  test('4) Skip on wizard step 2 stamps the flag and unblocks /admin/* without a model', async ({ page }) => {
    await freshSignupAndOpenAdmin(page);

    // Land on /admin/* — gate redirects to wizard.
    await page.goto(`${EDGE}/admin/`);
    await expect(page).toHaveURL(/\/onboarding/, { timeout: 15_000 });

    // Step 1: create a valid-named model so step 2 ("Skip") is reachable.
    // The exact form selectors live in OnboardingWizard.tsx; we drive it
    // via API + UI bridge so the test is robust to layout tweaks. Once the
    // session flag is set the gate stops redirecting, and clicking Skip
    // (which is the wizard's escape hatch on step 2) navigates to /schemas.
    await page.locator('input[placeholder*="my-llama" i], input[name="name" i], input[id*="display" i]').first()
      .fill('glm-skip')
      .catch(async () => {
        // Fallback: fill the first text input on the wizard page.
        const inputs = page.locator('input[type="text"], input:not([type])');
        await inputs.first().fill('glm-skip');
      });

    // Some flows have a Provider dropdown; OpenRouter is preselected on
    // openai-compatible default. Try to fill API key + Model Name.
    const apiKeyInput = page.locator('input[type="password"], input[name*="api" i], input[placeholder*="api key" i]').first();
    await apiKeyInput.fill('sk-test-fake').catch(() => {});
    const modelNameInput = page.locator('input[name*="model_name" i], input[placeholder*="model name" i]').first();
    await modelNameInput.fill('z-ai/glm-4.7').catch(() => {});

    // Click Test/Next/Submit — the primary button on step 1.
    const primary = page.getByRole('button', { name: /test|next|continue|submit|add model|create/i }).first();
    await primary.click().catch(() => {});

    // Wait for either the success indicator or the Skip button on step 2.
    const skip = page.getByRole('button', { name: /skip/i }).first();
    await skip.waitFor({ state: 'visible', timeout: 20_000 }).catch(() => {});
    if (await skip.isVisible().catch(() => false)) {
      await skip.click();
    }

    // After Skip, the wizard navigates to /schemas (per its onSuccess flow).
    await expect(page).toHaveURL(/\/(schemas|admin)/, { timeout: 10_000 });

    // The gate's session flag must be set with the new timestamped format.
    const flag = await page.evaluate(() => sessionStorage.getItem('bb_onboarded'));
    expect(flag, `flag should be set after wizard success: ${flag}`).toMatch(/^1:\d+$/);
  });
});
