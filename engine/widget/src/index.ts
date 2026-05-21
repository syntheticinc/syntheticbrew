import { WidgetUI, type WidgetConfig } from './ui';

/**
 * SyntheticBrew embeddable chat widget.
 *
 * Usage:
 *   <script src="https://your-engine/widget.js"
 *           data-schema="support-handbook"
 *           data-api-key="bb_pk_widget_abc123">
 *   </script>
 *
 * `data-schema` is the operator-declared schema name (engine 1.1.0+).
 * Widget 0.5.x and earlier accepted `data-schema-id` with a UUID — that
 * attribute is no longer recognised; redeploy the embed snippet with the
 * stable name from your `configApply.config` bundle.
 */

function findScriptTag(): HTMLScriptElement | null {
  // Find our own script tag — it should be the currently executing one.
  // document.currentScript works for synchronous scripts.
  if (document.currentScript instanceof HTMLScriptElement) {
    return document.currentScript;
  }

  // Fallback: find script with src containing "widget.js"
  const scripts = document.querySelectorAll<HTMLScriptElement>('script[src*="widget.js"]');
  if (scripts.length > 0) {
    return scripts[scripts.length - 1];
  }

  return null;
}

function resolveEndpoint(scriptEl: HTMLScriptElement, customEndpoint: string | null): string {
  // Explicit endpoint takes priority
  if (customEndpoint) {
    return customEndpoint.replace(/\/+$/, '');
  }

  // Default: derive from script src origin
  try {
    const url = new URL(scriptEl.src);
    return url.origin;
  } catch {
    // Relative src or invalid URL — use current page origin
    return window.location.origin;
  }
}

function readConfig(scriptEl: HTMLScriptElement): WidgetConfig {
  // dataset.schema reads the `data-schema` attribute (camelCase mapping).
  // Engine 1.1.0+ canonical name-keyed handle. Old `data-schema-id` (UUID)
  // is no longer accepted — operators must redeploy with the schema name
  // declared in their configApply bundle.
  const schemaName = scriptEl.dataset.schema;
  if (!schemaName) {
    throw new Error('[SyntheticBrew Widget] data-schema attribute is required');
  }

  const endpoint = resolveEndpoint(scriptEl, scriptEl.dataset.endpoint ?? null);

  return {
    schemaName,
    apiKey: scriptEl.dataset.apiKey ?? null,
    endpoint,
    position: scriptEl.dataset.position ?? 'bottom-right',
    theme: scriptEl.dataset.theme ?? 'light',
    title: scriptEl.dataset.title ?? 'Chat',
    primaryColor: scriptEl.dataset.primaryColor ?? null,
    welcomeMessage: scriptEl.dataset.welcome ?? null,
    placeholderText: scriptEl.dataset.placeholder ?? null,
  };
}

function init(): void {
  const scriptEl = findScriptTag();
  if (!scriptEl) {
    console.error('[SyntheticBrew Widget] Could not find widget script tag');
    return;
  }

  const config = readConfig(scriptEl);
  new WidgetUI(config);
}

// Initialize when DOM is ready
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', init);
} else {
  init();
}
