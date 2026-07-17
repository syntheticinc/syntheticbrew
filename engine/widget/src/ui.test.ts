// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { WidgetUI, type WidgetConfig } from './ui';
import { ChatClient } from './chat';

function widgetConfig(): WidgetConfig {
  return {
    schemaName: 'support',
    apiKey: null,
    endpoint: 'http://engine.test',
    position: 'bottom-right',
    theme: 'light',
    title: 'Chat',
    primaryColor: null,
    welcomeMessage: null,
    placeholderText: null,
  };
}

const flush = () => new Promise((resolve) => setTimeout(resolve, 0));

describe('WidgetUI — attribution badge', () => {
  // The widget attaches a CLOSED shadow root (host.shadowRoot is null), so
  // capture the root at creation time to inspect the internal DOM.
  let shadowRoot: ShadowRoot | null = null;
  const realFetch = globalThis.fetch;

  beforeEach(() => {
    shadowRoot = null;
    const origAttachShadow = Element.prototype.attachShadow;
    vi.spyOn(Element.prototype, 'attachShadow').mockImplementation(function (
      this: Element,
      init: ShadowRootInit,
    ) {
      shadowRoot = origAttachShadow.call(this, init);
      return shadowRoot;
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
    globalThis.fetch = realFetch;
    document.getElementById('syntheticbrew-widget')?.remove();
    try {
      localStorage.clear();
    } catch {
      // jsdom localStorage should always be available; ignore if not.
    }
  });

  it('renders the badge in the header when widget config enables attribution', async () => {
    vi.spyOn(ChatClient.prototype, 'fetchWidgetConfig').mockResolvedValue({ attribution: true });

    new WidgetUI(widgetConfig());
    await flush();

    const badge = shadowRoot!.querySelector<HTMLAnchorElement>('a.bb-attribution');
    expect(badge).not.toBeNull();
    expect(badge!.getAttribute('href')).toBe('https://syntheticbrew.ai?utm_source=widget');
    expect(badge!.getAttribute('target')).toBe('_blank');
    expect(badge!.getAttribute('rel')).toBe('nofollow noopener noreferrer');
    expect(badge!.textContent).toBe('Powered by SyntheticBrew');
    // Sits inside the header, right after the title.
    expect(badge!.parentElement?.className).toBe('bb-header');
    expect(badge!.previousElementSibling?.className).toBe('bb-header-title');
  });

  it('renders no badge element at all when attribution is disabled', async () => {
    vi.spyOn(ChatClient.prototype, 'fetchWidgetConfig').mockResolvedValue({ attribution: false });

    new WidgetUI(widgetConfig());
    await flush();

    expect(shadowRoot!.querySelector('.bb-attribution')).toBeNull();
  });

  it('renders no badge when the config fetch fails (fetchWidgetConfig fail-quiet path)', async () => {
    // fetchWidgetConfig never rejects by contract; a failed fetch resolves to
    // { attribution: false } — the UI must leave the header untouched.
    globalThis.fetch = vi.fn(async () => {
      throw new TypeError('network down');
    }) as unknown as typeof fetch;

    new WidgetUI(widgetConfig());
    await flush();

    expect(shadowRoot!.querySelector('.bb-attribution')).toBeNull();
  });
});
