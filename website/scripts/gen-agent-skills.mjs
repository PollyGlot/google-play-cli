#!/usr/bin/env node
// Generates the Agent Skills Discovery index served at
// /.well-known/agent-skills/index.json (Agent Skills Discovery RFC v0.2.0,
// https://github.com/cloudflare/agent-skills-discovery-rfc).
//
// gplay's agent skills live in the sibling repo PollyGlot/google-play-cli-skills
// (ADR-0021); this index makes them machine-discoverable from gplay.sh without
// duplicating their content — each entry's `url` points at the canonical
// SKILL.md on `main`, and `digest` is the SHA-256 of that artifact.
//
// The website build (CI) does NOT have the skills repo checked out, so this is
// a LOCAL dev tool: it reads a local clone of the skills repo and rewrites the
// committed JSON. Re-run it whenever the skills set changes, against a checkout
// that is in sync with the skills repo's `main`, then commit the result.
//
//   node scripts/gen-agent-skills.mjs [path-to-skills-repo/skills]
//
// Default skills dir is the conventional sibling clone (../google-play-cli-skills),
// overridable by argv[2] or $GPLAY_SKILLS_DIR.

import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = resolve(__dirname, '../..');

const SKILLS_DIR = resolve(
  process.argv[2] ??
    process.env.GPLAY_SKILLS_DIR ??
    join(REPO_ROOT, '..', 'google-play-cli-skills', 'skills'),
);

const OUT = resolve(__dirname, '../public/.well-known/agent-skills/index.json');

const RAW_BASE =
  'https://raw.githubusercontent.com/PollyGlot/google-play-cli-skills/main/skills';

// The foundation skill leads; the rest follow alphabetically — same order the
// skills repo README presents them in.
const FOUNDATION = 'gplay-cli-usage';

// Minimal frontmatter reader: the two keys we need (name, description) are each
// a single line. Good enough — this is not a general YAML parser.
function frontmatterField(text, key) {
  const fm = text.match(/^---\n([\s\S]*?)\n---/);
  if (!fm) throw new Error('missing frontmatter');
  const line = fm[1].match(new RegExp(`^${key}:\\s*(.+)$`, 'm'));
  if (!line) throw new Error(`missing frontmatter key: ${key}`);
  return line[1].trim().replace(/^["']|["']$/g, '');
}

function main() {
  if (!existsSync(SKILLS_DIR)) {
    console.error(
      `gen-agent-skills: skills dir not found: ${SKILLS_DIR}\n` +
        'Clone PollyGlot/google-play-cli-skills next to this repo, or pass its ' +
        'skills/ path as the first argument (or via $GPLAY_SKILLS_DIR).',
    );
    process.exit(1);
  }

  const names = readdirSync(SKILLS_DIR, { withFileTypes: true })
    .filter((d) => d.isDirectory())
    .map((d) => d.name)
    .sort((a, b) =>
      a === FOUNDATION ? -1 : b === FOUNDATION ? 1 : a.localeCompare(b),
    );

  const skills = names.map((name) => {
    const file = join(SKILLS_DIR, name, 'SKILL.md');
    const content = readFileSync(file);
    const hex = createHash('sha256').update(content).digest('hex');
    return {
      name,
      type: 'skill-md',
      description: frontmatterField(content.toString('utf8'), 'description'),
      url: `${RAW_BASE}/${name}/SKILL.md`,
      digest: `sha256:${hex}`,
    };
  });

  const index = {
    $schema: 'https://schemas.agentskills.io/discovery/0.2.0/schema.json',
    skills,
  };

  mkdirSync(dirname(OUT), { recursive: true });
  writeFileSync(OUT, JSON.stringify(index, null, 2) + '\n');
  console.log(
    `gen-agent-skills: wrote ${skills.length} skills to ${OUT} (from ${SKILLS_DIR})`,
  );
}

main();
