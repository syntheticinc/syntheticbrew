import { readdir, readFile, stat } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { dirname, join, relative, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../dist/', import.meta.url));
const errors = [];

async function walk(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = [];
  for (const entry of entries) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) files.push(...await walk(path));
    else files.push(path);
  }
  return files;
}

function routeFor(file) {
  const path = relative(root, file).split(sep).join('/');
  if (path === 'index.html') return '/';
  if (path.endsWith('/index.html')) return `/${path.slice(0, -11)}`;
  return `/${path.replace(/\.html$/, '')}`;
}

function text(value) {
  return value
    .replace(/<[^>]+>/g, '')
    .replaceAll('&amp;', '&')
    .replaceAll('&quot;', '"')
    .replaceAll('&#39;', "'")
    .replaceAll('&mdash;', '—')
    .trim();
}

const allFiles = await walk(root);
const htmlFiles = allFiles.filter((file) => file.endsWith('.html'));
const routes = new Set(htmlFiles.map(routeFor));
const expectedRoutes = [
  '/', '/build', '/download', '/examples', '/pricing', '/privacy', '/terms',
  '/knowledge-graphs', '/self-hosted', '/enterprise',
  '/compare', '/compare/dify', '/compare/langchain', '/compare/n8n', '/compare/crewai', '/compare/flowise', '/compare/langflow',
  '/blog', '/blog/dify-alternative-open-source-ai-agents', '/blog/langchain-vs-langgraph',
  '/blog/how-to-build-an-ai-agent', '/blog/what-are-ai-agents',
  '/features/ai-agent-builder', '/features/multi-agent-orchestration', '/features/mcp-integration',
  '/features/agent-memory', '/features/agentic-rag', '/features/ai-agent-observability',
  '/features/ai-agent-security', '/features/product-integrations',
  '/solutions/customer-service', '/solutions/ecommerce', '/solutions/banking',
  '/solutions/manufacturing-iot', '/solutions/saas-product-teams', '/404',
];

for (const route of expectedRoutes) {
  if (!routes.has(route)) errors.push(`Missing generated route: ${route}`);
}

// Routes served by other services in production. Each entry must be covered by
// a handle/redir in deploy/Caddyfile — verified below so the contract cannot drift.
const externalRoutePrefixes = [
  '/docs', '/register', '/login', '/dashboard', '/billing', '/settings', '/team',
  '/forgot-password', '/reset-password', '/verify-email', '/releases', '/api', '/demo',
  '/examples/', '/discord',
];

const caddyfile = await readFile(fileURLToPath(new URL('../deploy/Caddyfile', import.meta.url)), 'utf8');
for (const prefix of externalRoutePrefixes) {
  const bare = prefix.replace(/\/$/, '');
  if (!caddyfile.includes(bare)) errors.push(`External route prefix ${prefix} has no matching handle in deploy/Caddyfile`);
}

// Prod/preview Caddyfile parity for the agent-readiness surface: both must carry
// the markdown-negotiation matcher and the agent-skills Content-Type handling.
const previewCaddyfile = await readFile(fileURLToPath(new URL('../deploy/Caddyfile.preview', import.meta.url)), 'utf8');
for (const [label, config] of [['deploy/Caddyfile', caddyfile], ['deploy/Caddyfile.preview', previewCaddyfile]]) {
  if (!config.includes('header Accept *text/markdown*')) errors.push(`${label}: missing Accept: text/markdown negotiation matcher`);
  if (!config.includes('agent-skills')) errors.push(`${label}: missing agent-skills handling`);
}

for (const file of htmlFiles) {
  const route = routeFor(file);
  const html = await readFile(file, 'utf8');
  if (route === '/discord') continue;
  const titleMatch = html.match(/<title>([\s\S]*?)<\/title>/i);
  const descriptionMatch = html.match(/<meta\s+name="description"\s+content="([^"]*)"/i);
  const canonicalMatch = html.match(/<link\s+rel="canonical"\s+href="([^"]+)"/i);
  const h1Count = (html.match(/<h1(?:\s|>)/gi) || []).length;

  if (!titleMatch) errors.push(`${route}: missing title`);
  else {
    const titleValue = text(titleMatch[1]);
    if (titleValue.length > 60) errors.push(`${route}: title is ${titleValue.length} characters`);
    if (titleValue.length < 15) errors.push(`${route}: title is unusually short`);
  }

  if (!descriptionMatch) errors.push(`${route}: missing meta description`);
  else {
    const description = text(descriptionMatch[1]);
    if (description.length > 160) errors.push(`${route}: description is ${description.length} characters`);
    if (description.length < 70 && route !== '/404') errors.push(`${route}: description is only ${description.length} characters`);
  }

  if (!canonicalMatch) errors.push(`${route}: missing canonical URL`);
  if (h1Count !== 1) errors.push(`${route}: expected one H1, found ${h1Count}`);

  for (const image of html.matchAll(/<img\b[^>]*>/gi)) {
    if (!/\salt="[^"]*"/i.test(image[0])) errors.push(`${route}: image without alt attribute`);
  }

  for (const block of html.matchAll(/<script\s+type="application\/ld\+json"[^>]*>([\s\S]*?)<\/script>/gi)) {
    try { JSON.parse(block[1]); }
    catch { errors.push(`${route}: invalid JSON-LD`); }
  }

  for (const match of html.matchAll(/href="([^"]+)"/gi)) {
    const href = match[1];
    if (!href.startsWith('/') || href.startsWith('//')) continue;
    const clean = href.split(/[?#]/)[0].replace(/\/$/, '') || '/';
    if (routes.has(clean)) continue;
    if (externalRoutePrefixes.some((prefix) => clean === prefix || clean.startsWith(prefix))) continue;
    const assetPath = join(root, clean.slice(1));
    try { if ((await stat(assetPath)).isFile()) continue; } catch {}
    errors.push(`${route}: unresolved internal link ${href}`);
  }
}

for (const required of [
  'robots.txt', 'llms.txt', 'auth.md', 'sitemap-index.xml',
  '.well-known/api-catalog', '.well-known/mcp/server-card.json',
  '.well-known/agent-skills/index.json',
  '.well-known/agent-skills/syntheticbrew-docs-mcp/SKILL.md',
  '.well-known/agent-skills/syntheticbrew-cloud-auth/SKILL.md',
]) {
  try { await stat(join(root, required)); }
  catch { errors.push(`Missing public discovery file: ${required}`); }
}

// auth.md must keep the Auth.md-spec heading the agent-readiness scanner checks for.
try {
  const authMd = await readFile(join(root, 'auth.md'), 'utf8');
  if (authMd.split('\n', 1)[0].trim() !== '# auth.md') errors.push('auth.md: first line must be "# auth.md"');
} catch {}

// Every HTML page needs a non-empty markdown mirror for Accept: text/markdown negotiation.
for (const file of htmlFiles) {
  if (!file.endsWith(`${sep}index.html`)) continue;
  try {
    const mirror = await readFile(join(dirname(file), 'index.md'), 'utf8');
    if (!mirror.trim()) errors.push(`${routeFor(file)}: markdown mirror is empty`);
  } catch {
    errors.push(`${routeFor(file)}: missing markdown mirror index.md`);
  }
}

// Agent Skills index digests must match the served SKILL.md bytes.
try {
  const index = JSON.parse(await readFile(join(root, '.well-known/agent-skills/index.json'), 'utf8'));
  if (index.$schema !== 'https://schemas.agentskills.io/discovery/0.2.0/schema.json') errors.push('agent-skills index.json: unexpected $schema');
  for (const skill of index.skills ?? []) {
    try {
      const bytes = await readFile(join(root, '.well-known/agent-skills', skill.name, 'SKILL.md'));
      const digest = `sha256:${createHash('sha256').update(bytes).digest('hex')}`;
      if (skill.digest !== digest) errors.push(`agent-skills index.json: digest drift for ${skill.name}`);
    } catch {
      errors.push(`agent-skills index.json: references missing skill ${skill.name}`);
    }
  }
} catch {}

// The WebMCP registration script must not silently drop out of the layout.
if (htmlFiles.length > 0) {
  const homepage = await readFile(join(root, 'index.html'), 'utf8').catch(() => '');
  if (!homepage.includes('navigator.modelContext')) errors.push('index.html: WebMCP script (navigator.modelContext) missing from layout output');
}

if (errors.length) {
  console.error(`Build audit failed with ${errors.length} issue(s):`);
  for (const error of [...new Set(errors)]) console.error(`- ${error}`);
  process.exit(1);
}

console.log(`Build audit passed: ${htmlFiles.length} HTML pages, ${expectedRoutes.length} required routes, metadata, JSON-LD, image alt text, links, and crawler files checked.`);
