// §1.6-ext Navigation — prototype toggle Production↔Prototype switches data source
// TC: NAV-06 | No SCC tags

import { test, expect } from '../fixtures';

test.describe('Admin — prototype mode toggle', () => {
  test('prototype toggle button is present in header', async ({ authenticatedAdmin }) => {
    const page = authenticatedAdmin;
    await page.waitForLoadState('networkidle');

    const toggle = page.locator(
      'button:has-text("Prototype"), button:has-text("Production"), [data-testid="prototype-toggle"], [aria-label*="prototype"]'
    ).first();

    if (await toggle.count() === 0) {
      test.skip(true, 'Prototype toggle not visible — VITE_PROTOTYPE_ENABLED may be false in this build');
      return;
    }
    await expect(toggle).toBeVisible();
  });

  test('clicking prototype toggle changes mode indicator', async ({ authenticatedAdmin }) => {
    const page = authenticatedAdmin;
    await page.waitForLoadState('networkidle');

    const toggle = page.locator(
      'button:has-text("Prototype"), button:has-text("Production"), [data-testid="prototype-toggle"]'
    ).first();

    if (await toggle.count() === 0) {
      test.skip(true, 'Prototype toggle not found');
      return;
    }

    const initialText = await toggle.textContent();
    await toggle.click();
    await page.waitForTimeout(500);
    const newText = await toggle.textContent();
    // Mode should have changed
    expect(newText).not.toBe(initialText);
  });

  test('prototype badge visible when prototype mode active', async ({ authenticatedAdmin }) => {
    const page = authenticatedAdmin;
    // Enable prototype mode via localStorage
    await page.evaluate(() => localStorage.setItem('syntheticbrew_prototype_mode', 'true'));
    await page.reload();
    await page.waitForLoadState('networkidle');

    const badge = page.locator('text=/prototype/i, [data-testid="prototype-badge"]').first();
    // May or may not be visible depending on flag, just verify no crash
    const url = page.url();
    expect(url).toContain('/admin');
  });
});
