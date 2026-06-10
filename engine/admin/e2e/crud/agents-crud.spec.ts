// §1.7 CRUD — Agents: create, edit system_prompt, delete
// TC: CRUD-02 | SCC-01

import { test, expect, apiFetch } from '../fixtures';

test.describe('Agents CRUD', () => {
  test('create agent via API', async ({ request, adminToken }) => {
    const name = `test-agent-${Date.now()}`;
    const res = await apiFetch(request, '/agents', {
      method: 'POST',
      token: adminToken,
      body: {
        name,
        system_prompt: 'You are a test assistant.',
        public: false,
      },
    });
    expect([200, 201]).toContain(res.status());
    const body = await res.json();
    expect(body.name ?? body.id).toBeTruthy();

    // Teardown
    await apiFetch(request, `/agents/${name}`, { method: 'DELETE', token: adminToken });
  });

  test('agent appears in GET /agents list', async ({ request, adminToken }) => {
    const name = `list-agent-${Date.now()}`;
    await apiFetch(request, '/agents', {
      method: 'POST',
      token: adminToken,
      body: { name, system_prompt: 'Test', public: false },
    });

    const listRes = await apiFetch(request, '/agents', { token: adminToken });
    expect(listRes.status()).toBe(200);
    const body = await listRes.json();
    const agents = Array.isArray(body) ? body : (body.agents ?? body.data ?? []);
    expect(agents.some((a: { name: string }) => a.name === name)).toBe(true);

    await apiFetch(request, `/agents/${name}`, { method: 'DELETE', token: adminToken });
  });

  test('update agent system_prompt', async ({ request, adminToken }) => {
    const name = `upd-agent-${Date.now()}`;
    await apiFetch(request, '/agents', {
      method: 'POST',
      token: adminToken,
      body: { name, system_prompt: 'Original prompt', public: false },
    });

    const updRes = await apiFetch(request, `/agents/${name}`, {
      method: 'PUT',
      token: adminToken,
      body: { name, system_prompt: 'Updated prompt', public: false },
    });
    expect([200, 204]).toContain(updRes.status());

    const getRes = await apiFetch(request, `/agents/${name}`, { token: adminToken });
    const body = await getRes.json();
    expect(body.system_prompt).toBe('Updated prompt');

    await apiFetch(request, `/agents/${name}`, { method: 'DELETE', token: adminToken });
  });

  test('update agent max_step_duration round-trips via API', async ({ request, adminToken }) => {
    const name = `step-agent-${Date.now()}`;
    await apiFetch(request, '/agents', {
      method: 'POST',
      token: adminToken,
      body: { name, system_prompt: 'Test', public: false },
    });

    const updRes = await apiFetch(request, `/agents/${name}`, {
      method: 'PUT',
      token: adminToken,
      body: { name, system_prompt: 'Test', max_step_duration: 90, public: false },
    });
    expect([200, 204]).toContain(updRes.status());

    const getRes = await apiFetch(request, `/agents/${name}`, { token: adminToken });
    const body = await getRes.json();
    expect(body.max_step_duration).toBe(90);

    await apiFetch(request, `/agents/${name}`, { method: 'DELETE', token: adminToken });
  });

  test('delete agent removes it from list', async ({ request, adminToken }) => {
    const name = `del-agent-${Date.now()}`;
    await apiFetch(request, '/agents', {
      method: 'POST',
      token: adminToken,
      body: { name, system_prompt: 'Test', public: false },
    });
    await apiFetch(request, `/agents/${name}`, { method: 'DELETE', token: adminToken });

    const listRes = await apiFetch(request, '/agents', { token: adminToken });
    const body = await listRes.json();
    const agents = Array.isArray(body) ? body : (body.agents ?? body.data ?? []);
    expect(agents.some((a: { name: string }) => a.name === name)).toBe(false);
  });

  test('agents page renders without error', async ({ authenticatedAdmin }) => {
    const page = authenticatedAdmin;
    await page.goto('/admin/agents');
    await page.waitForLoadState('networkidle');
    await expect(page.locator('text=/Something went wrong/i')).not.toBeVisible();
  });
});
