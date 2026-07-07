import { describe, it, expect } from 'vitest';
import { renderMarkdown } from './markdown';

describe('renderMarkdown link safety', () => {
  it('renders http/https/mailto and relative links as anchors', () => {
    expect(renderMarkdown('[a](https://example.com)')).toContain('href="https://example.com"');
    expect(renderMarkdown('[a](http://example.com)')).toContain('href="http://example.com"');
    expect(renderMarkdown('[m](mailto:a@b.com)')).toContain('href="mailto:a@b.com"');
    expect(renderMarkdown('[r](/docs/page)')).toContain('href="/docs/page"');
    expect(renderMarkdown('[h](#section)')).toContain('href="#section"');
  });

  it('drops disallowed-scheme URLs (javascript:, data:, vbscript:) and keeps the text', () => {
    const payloads = [
      'javascript:alert1',
      'JavaScript:alert1',
      'java\tscript:alert1', // control-char obfuscation browsers ignore in schemes
      'data:text/html,x',
      'vbscript:msgbox',
    ];
    for (const bad of payloads) {
      const out = renderMarkdown(`[click](${bad})`);
      expect(out).not.toContain('href="');
      expect(out).not.toMatch(/javascript:|data:|vbscript:/i);
      expect(out).toContain('click'); // visible text is preserved
    }
  });
});
