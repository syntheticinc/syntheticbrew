import { test as base, expect, Page, APIRequestContext } from '@playwright/test';

export { expect };

export const BASE_URL = process.env.PLAYWRIGHT_BASE_URL ?? 'http://localhost:18082';
export const CLOUD_API = `${BASE_URL}/api/v1`;
export const ENGINE_API = `${BASE_URL}/api/v1`;

export type AdminSession = {
  engineToken: string;
  cloudAccessToken: string;
  email: string;
  password: string;
  available: boolean;
  blockedReason?: string;
};

function randomEmail(): string {
  return `pw-admin-${Date.now()}-${Math.random().toString(36).slice(2, 10)}@e2e.syntheticbrew.local`;
}

async function cloudRegister(request: APIRequestContext, email: string, password: string) {
  const res = await request.post(`${CLOUD_API}/auth/register`, { data: { email, password } });
  if (res.status() === 429) throw new Error('RATE_LIMITED');
  return res.status();
}

async function cloudLogin(request: APIRequestContext, email: string, password: string) {
  const res = await request.post(`${CLOUD_API}/auth/login`, { data: { email, password } });
  return { status: res.status(), body: await res.json().catch(() => ({})) };
}

async function mintEngineToken(request: APIRequestContext, cloudAccess: string) {
  const res = await request.post(`${CLOUD_API}/auth/engine-token`, {
    headers: { Authorization: `Bearer ${cloudAccess}` },
  });
  return { status: res.status(), body: await res.json().catch(() => ({})) };
}

type WorkerFixtures = {
  adminSession: AdminSession;
};

type TestFixtures = {
  adminToken: string;
  authenticatedAdmin: Page;
};

export const test = base.extend<TestFixtures, WorkerFixtures>({
  adminSession: [
    async ({ browser }, use) => {
      const request = await browser.newContext().then(c => c.request);
      const email = randomEmail();
      const password = 'AdminE2e!-' + Math.random().toString(36).slice(2, 10);
      const session: AdminSession = {
        engineToken: '',
        cloudAccessToken: '',
        email,
        password,
        available: false,
      };
      try {
        const regStatus = await cloudRegister(request, email, password);
        if (regStatus !== 201 && regStatus !== 409) {
          session.blockedReason = `register_${regStatus}`;
          await use(session);
          return;
        }
        const login = await cloudLogin(request, email, password);
        if (login.status !== 200) {
          session.blockedReason = login.body?.error?.code
            ? `${login.body.error.code} — stack requires SMTP mock or auto-verify`
            : `login_${login.status}`;
          await use(session);
          return;
        }
        // cloud-api wraps responses as { data: { access_token, refresh_token, ... } }
        session.cloudAccessToken = login.body?.data?.access_token ?? login.body?.access_token ?? '';
        const engine = await mintEngineToken(request, session.cloudAccessToken);
        if (engine.status !== 200) {
          session.blockedReason = `engine_token_${engine.status}: ${JSON.stringify(engine.body).slice(0, 200)}`;
          await use(session);
          return;
        }
        // engine-token response shape: { data: { token: "..." } }
        session.engineToken = engine.body?.data?.token ?? engine.body?.data?.engine_token ?? engine.body?.token ?? engine.body?.engine_token ?? engine.body?.access_token ?? '';
        session.available = !!session.engineToken;
        if (!session.available) session.blockedReason = 'engine_token_empty';
        // Seed a default chat model so agent creation (which now requires a
        // resolvable model — see resolveAgentModel C1 fix) succeeds without
        // each test repeating the boilerplate. The model carries
        // is_default=true so resolveAgentModel falls back to it when
        // model/model_id is omitted.
        if (session.available) {
          const seed = await request.post(`${ENGINE_API}/models`, {
            headers: { Authorization: `Bearer ${session.engineToken}`, 'Content-Type': 'application/json' },
            data: {
              name: 'e2e-default-chat',
              type: 'openai_compatible',
              kind: 'chat',
              model_name: 'e2e-test-model',
              api_key: 'e2e-test-key',
              base_url: 'https://api.test.example',
              is_default: true,
            },
          });
          if (seed.status() !== 201 && seed.status() !== 200 && seed.status() !== 409) {
            session.blockedReason = `seed_default_model_${seed.status()}: ${(await seed.text()).slice(0, 200)}`;
            session.available = false;
          }
        }
      } catch (e) {
        session.blockedReason = (e as Error).message;
      }
      await use(session);
    },
    { scope: 'worker' },
  ],
  adminToken: async ({ adminSession }, use, testInfo) => {
    if (!adminSession.available) {
      testInfo.skip(true, `admin auth unavailable: ${adminSession.blockedReason ?? 'no token'}`);
      return;
    }
    await use(adminSession.engineToken);
  },
  authenticatedAdmin: async ({ page, adminSession }, use, testInfo) => {
    if (!adminSession.available) {
      testInfo.skip(true, `admin auth unavailable: ${adminSession.blockedReason ?? 'no token'}`);
      return;
    }
    const tok = adminSession.engineToken;
    await page.addInitScript((token: string) => {
      window.localStorage.setItem('jwt', token);
      window.localStorage.setItem('access_token', token);
    }, tok);
    await page.goto('/admin/');
    await use(page);
  },
});

export async function apiFetch(
  request: APIRequestContext,
  path: string,
  options: { method?: string; token?: string; body?: unknown; headers?: Record<string, string> } = {}
) {
  const url = path.startsWith('http') ? path : `${ENGINE_API}${path}`;
  return await request.fetch(url, {
    method: options.method ?? 'GET',
    headers: {
      'Content-Type': 'application/json',
      ...(options.token ? { Authorization: `Bearer ${options.token}` } : {}),
      ...options.headers,
    },
    data: options.body,
  });
}
