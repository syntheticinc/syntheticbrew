// §1.8-ext Widget — prototype preview updates without API round-trip
// TC: WID-04

import { test, expect } from '../fixtures';

test.describe('Widget — prototype preview', () => {
  test.skip(true, 'Prototype preview requires VITE_PROTOTYPE_ENABLED=true build — skip in production build');

  test('changing color in prototype mode updates preview without API call', async ({ authenticatedAdmin }) => {
    const page = authenticatedAdmin;

    // Enable prototype mode
    await page.evaluate(() => localStorage.setItem('syntheticbrew_prototype_mode', 'true'));
    await page.goto('/admin/widget');
    await page.waitForLoadState('networkidle');

    const apiRequests: string[] = [];
    page.on('request', req => {
      if (req.url().includes('/api/v1/settings')) apiRequests.push(req.url());
    });

    const colorInput = page.locator('input[type="color"], input[name*="color"]').first();
    if (await colorInput.count() > 0) {
      const beforeCount = apiRequests.length;
      await colorInput.fill('#ff0000');
      await page.waitForTimeout(500);
      // In prototype mode, no API call should be made for preview update
      expect(apiRequests.length).toBe(beforeCount);
    }
  });
});
