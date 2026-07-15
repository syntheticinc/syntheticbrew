// Generates dist/.well-known/agent-skills/index.json (Agent Skills Discovery
// RFC v0.2.0) from the SKILL.md files that astro build copied out of public/.
// Digests are computed from the dist bytes actually served, so the index can
// never drift from the artifacts. Runs after `astro build`, before the audit.
import { readdir, readFile, writeFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const SITE_URL = 'https://syntheticbrew.ai';
const skillsRoot = fileURLToPath(new URL('../dist/.well-known/agent-skills/', import.meta.url));

function frontmatterField(source, field) {
  const match = source.match(new RegExp(`^${field}:\\s*(.+)$`, 'm'));
  return match ? match[1].trim() : null;
}

const entries = await readdir(skillsRoot, { withFileTypes: true });
const skills = [];

for (const entry of entries.filter((item) => item.isDirectory()).sort((a, b) => a.name.localeCompare(b.name))) {
  const skillPath = join(skillsRoot, entry.name, 'SKILL.md');
  const bytes = await readFile(skillPath);
  const source = bytes.toString('utf8');
  const name = frontmatterField(source, 'name');
  const description = frontmatterField(source, 'description');
  if (!name || !description) {
    console.error(`build-skills-index: ${entry.name}/SKILL.md is missing name or description frontmatter`);
    process.exit(1);
  }
  if (name !== entry.name) {
    console.error(`build-skills-index: frontmatter name "${name}" does not match directory "${entry.name}"`);
    process.exit(1);
  }
  skills.push({
    name,
    type: 'skill-md',
    description,
    url: `${SITE_URL}/.well-known/agent-skills/${name}/SKILL.md`,
    digest: `sha256:${createHash('sha256').update(bytes).digest('hex')}`,
  });
}

if (skills.length === 0) {
  console.error('build-skills-index: no SKILL.md files found under dist/.well-known/agent-skills/');
  process.exit(1);
}

const index = {
  $schema: 'https://schemas.agentskills.io/discovery/0.2.0/schema.json',
  skills,
};

await writeFile(join(skillsRoot, 'index.json'), `${JSON.stringify(index, null, 2)}\n`);
console.log(`build-skills-index: wrote index.json with ${skills.length} skill(s).`);
