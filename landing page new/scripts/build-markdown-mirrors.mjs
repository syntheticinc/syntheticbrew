// Generates a markdown mirror (index.md) next to every dist/**/index.html so
// Caddy can serve markdown to clients sending `Accept: text/markdown` (see the
// @markdown matcher in deploy/Caddyfile). Runs after `astro build`, before the
// audit, which enforces that every HTML page has a non-empty mirror.
import { readdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, join, relative, sep } from 'node:path';
import { fileURLToPath } from 'node:url';
import { unified } from 'unified';
import rehypeParse from 'rehype-parse';
import rehypeRemark from 'rehype-remark';
import remarkGfm from 'remark-gfm';
import remarkStringify from 'remark-stringify';
import { select, selectAll } from 'hast-util-select';
import { remove } from 'unist-util-remove';

const root = fileURLToPath(new URL('../dist/', import.meta.url));
const STRIP_TAGS = new Set(['script', 'style', 'noscript', 'svg', 'form', 'button', 'iframe', 'template']);

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

function headMeta(hast) {
  const title = select('head > title', hast);
  const description = selectAll('head > meta', hast)
    .find((node) => node.properties?.name === 'description');
  const canonical = selectAll('head > link', hast)
    .find((node) => node.properties?.rel?.includes('canonical'));
  return {
    title: title?.children?.[0]?.value?.trim() ?? '',
    description: description?.properties?.content ?? '',
    canonical: canonical?.properties?.href ?? '',
  };
}

const parser = unified().use(rehypeParse);
const converter = unified().use(rehypeRemark).use(remarkGfm).use(remarkStringify, { bullet: '-', fences: true });

const htmlFiles = (await walk(root)).filter((file) => file.endsWith(`${sep}index.html`) || relative(root, file) === 'index.html');
let written = 0;

for (const file of htmlFiles) {
  const html = await readFile(file, 'utf8');
  const hast = parser.parse(html);
  const meta = headMeta(hast);
  const main = select('main', hast) ?? select('body', hast);
  if (!main) {
    console.error(`build-markdown-mirrors: no <main> or <body> in ${relative(root, file)}`);
    process.exit(1);
  }
  remove(main, (node) => node.type === 'comment' || (node.type === 'element' && STRIP_TAGS.has(node.tagName)));
  const tree = await converter.run(main);
  const markdown = converter.stringify(tree).trim();
  if (!markdown) {
    console.error(`build-markdown-mirrors: empty markdown output for ${relative(root, file)}`);
    process.exit(1);
  }
  const frontmatter = [
    '---',
    `title: ${JSON.stringify(meta.title)}`,
    `description: ${JSON.stringify(meta.description)}`,
    `canonical: ${meta.canonical}`,
    '---',
    '',
  ].join('\n');
  await writeFile(join(dirname(file), 'index.md'), `${frontmatter}\n${markdown}\n`);
  written += 1;
}

if (written === 0) {
  console.error('build-markdown-mirrors: no index.html files found in dist/');
  process.exit(1);
}

console.log(`build-markdown-mirrors: wrote ${written} markdown mirror(s).`);
