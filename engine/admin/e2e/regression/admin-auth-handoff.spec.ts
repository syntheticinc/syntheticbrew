// Cross-app auth handoff: after the user logs in via cloud-web-spa, the
// admin SPA at /admin/ must already have an engine-token (or be able to
// mint one transparently) so visiting /admin/ does NOT bounce back to
// /login.
//
// Catches F8: cloud-web-spa stores `syntheticbrew_access_token` (cloud auth)
// in localStorage but admin SPA reads `jwt` (engine token). Without a
// post-login bridge that puts an engine token into the right key,
// every fresh visit to /admin/ → infinite redirect loop.

import { test, expect, BASE_URL } from '../fixtures';

test.describe('Regression — admin auth handoff after cloud-web-spa login', () => {
  test('after login, navigating to /admin/ does not bounce back to /login', async ({ adminSession, page }) => {
    if (!adminSession.available) {
      test.skip(true, `cannot sign-in: ${adminSession.blockedReason ?? 'no session'}`);
      return;
    }

    // Step 1: simulate a real login on /login (NOT inject token directly).
    // 30s waitFor accommodates Vite dev cold-compile of /login on the first
    // hit after a Tilt stack restart.
    await page.goto(`${BASE_URL}/login`);
    const email = page.locator('input[type="email"], input[placeholder*="@" i]');
    await email.waitFor({ state: 'visible', timeout: 30_000 });
    await email.fill(adminSession.email);
    await page.locator('input[type="password"]').fill(adminSession.password);
    await page.getByRole('button', { name: /sign in/i }).click();
    await page.waitForLoadState('networkidle');

    // Step 2: now navigate to /admin/. If F8 lives, this redirects back
    // to /login because admin SPA can't find `jwt` in localStorage.
    await page.goto(`${BASE_URL}/admin/`);
    await page.waitForLoadState('networkidle');

    expect(
      page.url(),
      `F8: cloud-web-spa stores syntheticbrew_access_token but admin SPA reads "jwt"; ` +
        `after a clean login the user gets bounced back to /login at ${page.url()}.`,
    ).not.toContain('/login');
  });
});
