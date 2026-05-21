// §1.17 Metering — quota exhausted: steps_used >= quota → next chat → 402
// TC: MET-01

import { test, expect, apiFetch } from '../fixtures';

test.describe('Metering — quota 402', () => {
  test.skip(true, '§1.17: Quota testing requires Cloud metering stack with test provisioning — skip in CE stack');

  test('chat after quota exhaustion returns 402 Payment Required', async ({ request, adminToken }) => {
    // Requires SYNTHETICBREW_METERING_URL configured and quota set to 0
    const res = await apiFetch(request, '/schemas/test-schema/chat', {
      method: 'POST',
      token: adminToken,
      body: { message: 'Hello' },
    });
    expect(res.status()).toBe(402);
    const body = await res.json();
    expect(JSON.stringify(body)).toMatch(/quota|exceeded|limit/i);
  });
});
