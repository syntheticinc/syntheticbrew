/**
 * Basic markdown to HTML converter.
 * Supports: bold, inline code, code blocks, links, lists, newlines.
 */
export function renderMarkdown(text: string): string {
  // Escape HTML entities first
  let html = escapeHtml(text);

  // Code blocks: ```...```
  html = html.replace(/```(\w*)\n?([\s\S]*?)```/g, (_match, _lang, code) => {
    return `<pre><code>${code.trim()}</code></pre>`;
  });

  // Inline code: `...`
  html = html.replace(/`([^`\n]+)`/g, '<code>$1</code>');

  // Bold: **...** or __...__
  html = html.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
  html = html.replace(/__([^_]+)__/g, '<strong>$1</strong>');

  // Links: [text](url) — only render an anchor for an allowlisted scheme
  // (http/https/mailto) or a scheme-less/relative URL. Prompt-injected agent
  // output could otherwise smuggle a `javascript:`/`data:` URL that runs script
  // in the host page on click, so drop the link and keep the (escaped) text.
  html = html.replace(
    /\[([^\]]+)\]\(([^)]+)\)/g,
    (_match, label, url) =>
      isSafeLinkUrl(url)
        ? `<a href="${url}" target="_blank" rel="noopener noreferrer">${label}</a>`
        : label,
  );

  // Process lines for lists and paragraphs
  html = processLines(html);

  return html;
}

function escapeHtml(text: string): string {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

// isSafeLinkUrl allows http/https/mailto and scheme-less (relative/anchor) URLs
// and rejects everything else (javascript:, data:, vbscript:, …). Browsers
// ignore ASCII whitespace/control chars inside a scheme when resolving an href,
// so strip them before checking to defeat obfuscation like `java\tscript:`.
function isSafeLinkUrl(url: string): boolean {
  const normalized = url.replace(/[\x00-\x20]/g, '').toLowerCase();
  if (/^(https?:|mailto:)/.test(normalized)) return true;
  // A leading `scheme:` we did not allow → reject. No scheme → relative → allow.
  return !/^[a-z][a-z0-9+.-]*:/.test(normalized);
}

function processLines(html: string): string {
  const lines = html.split('\n');
  const result: string[] = [];
  let inList = false;

  for (const line of lines) {
    const trimmed = line.trim();

    // Skip empty lines inside <pre> blocks (already handled)
    if (trimmed.startsWith('<pre>') || trimmed.endsWith('</pre>')) {
      if (inList) {
        result.push('</ul>');
        inList = false;
      }
      result.push(line);
      continue;
    }

    // List items: - item or * item
    if (/^[-*]\s+/.test(trimmed)) {
      if (!inList) {
        result.push('<ul>');
        inList = true;
      }
      result.push(`<li>${trimmed.replace(/^[-*]\s+/, '')}</li>`);
      continue;
    }

    // Close list if we're no longer in one
    if (inList) {
      result.push('</ul>');
      inList = false;
    }

    // Empty line
    if (trimmed === '') {
      continue;
    }

    // Regular line
    result.push(line + '<br>');
  }

  if (inList) {
    result.push('</ul>');
  }

  // Remove trailing <br>
  const joined = result.join('\n');
  return joined.replace(/<br>\s*$/, '');
}
