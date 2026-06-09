#!/usr/bin/env node
// Generates the CLI command reference from the real `gplay --help` output.
//
// Source of truth is the compiled binary: every page is parsed from the help
// text cobra prints, so the reference can never drift from the shipped CLI.
// Output is one Starlight Markdown page per command (groups get index.md),
// plus src/data/cli-surface.json with exact surface counts for the landing
// page. The whole output directory is regenerated from scratch on each run
// and is gitignored — `npm run build` triggers this via the prebuild hook.
//
// Usage: node scripts/gen-reference.mjs [path-to-gplay-binary]

import { execFileSync } from 'node:child_process';
import { existsSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = resolve(__dirname, '../..');
const BIN = resolve(process.argv[2] ?? join(REPO_ROOT, 'bin/gplay'));
const OUT = resolve(__dirname, '../src/content/docs/docs/reference');
const DATA_OUT = resolve(__dirname, '../src/data/cli-surface.json');
const SKIP = new Set(['help', 'completion']);

function ensureBinary() {
  if (existsSync(BIN)) return;
  console.log(`gen-reference: ${BIN} not found, building it with go build...`);
  execFileSync('go', ['build', '-o', BIN, './cmd/gplay'], {
    cwd: REPO_ROOT,
    stdio: 'inherit',
  });
}

function helpText(cmdPath) {
  return execFileSync(BIN, [...cmdPath, '--help'], { encoding: 'utf8' });
}

// Split cobra help output into its named sections. Everything before the
// first section header is the long description.
const SECTION_HEADERS = [
  'Usage:',
  'Aliases:',
  'Examples:',
  'Available Commands:',
  'Flags:',
  'Global Flags:',
  'Additional help topics:',
];

function parseHelp(text) {
  const lines = text.split('\n');
  const sections = { long: [] };
  let current = 'long';
  for (const line of lines) {
    const header = SECTION_HEADERS.find((h) => line.startsWith(h));
    if (header) {
      current = header;
      sections[current] = [];
      continue;
    }
    if (line.startsWith('Use "')) break; // trailing cobra hint
    sections[current].push(line);
  }
  const trim = (arr) => (arr ?? []).join('\n').replace(/^\n+|\s+$/g, '');
  return {
    long: trim(sections.long),
    usage: trim(sections['Usage:']),
    examples: trim(sections['Examples:']),
    commands: parseCommandList(sections['Available Commands:'] ?? []),
    flags: parseFlags(sections['Flags:'] ?? []),
    globalFlags: parseFlags(sections['Global Flags:'] ?? []),
  };
}

function parseCommandList(lines) {
  const out = [];
  for (const line of lines) {
    const m = line.match(/^ {2}(\S+)\s{2,}(.*)$/);
    if (m && !SKIP.has(m[1])) out.push({ name: m[1], short: m[2].trim() });
  }
  return out;
}

// Flags print as two columns separated by 2+ spaces; continuation lines
// (no flag spec of their own) extend the previous description.
function parseFlags(lines) {
  const out = [];
  for (const line of lines) {
    if (!line.trim()) continue;
    const m = line.match(/^\s+(-{1,2}\S.*?)\s{2,}(.*)$/);
    if (m) {
      out.push({ spec: m[1].trim(), desc: m[2].trim() });
    } else if (out.length > 0) {
      out[out.length - 1].desc += ' ' + line.trim();
    }
  }
  return out;
}

const tableEscape = (s) =>
  s.replace(/\|/g, '\\|').replace(/</g, '&lt;').replace(/>/g, '&gt;');

// Escape `<` in prose so Markdown never swallows placeholders like <aab>
// as inline HTML — but leave code spans (`...`) untouched.
function proseEscape(text) {
  return text
    .split(/(`[^`]*`)/)
    .map((part) => (part.startsWith('`') ? part : part.replace(/</g, '\\<')))
    .join('');
}

function flagsTable(flags) {
  if (flags.length === 0) return '';
  const rows = flags
    .map((f) => `| \`${f.spec.replace(/`/g, '')}\` | ${tableEscape(f.desc)} |`)
    .join('\n');
  return `| Flag | Description |\n| --- | --- |\n${rows}`;
}

// First sentence of the long description, flattened — used as the SEO/GEO
// meta description for the page.
function metaDescription(cmd, long) {
  const flat = long.replace(/\s+/g, ' ').trim();
  const first = flat.match(/^.*?[.!?](\s|$)/)?.[0]?.trim() ?? flat;
  const base = first.length > 20 ? first : flat;
  const desc = `${cmd} — ${base}`;
  return desc.length > 158 ? desc.slice(0, 155).trimEnd() + '…' : desc;
}

function renderPage(cmdPath, parsed) {
  const full = ['gplay', ...cmdPath].join(' ');
  const label = cmdPath.length === 0 ? 'gplay (root)' : cmdPath[cmdPath.length - 1];
  const isGroup = parsed.commands.length > 0;

  let body = '';
  if (parsed.long) body += `${proseEscape(parsed.long)}\n\n`;
  if (parsed.usage) body += `## Usage\n\n\`\`\`txt\n${parsed.usage}\n\`\`\`\n\n`;
  if (parsed.examples) body += `## Examples\n\n\`\`\`sh\n${parsed.examples}\n\`\`\`\n\n`;

  if (isGroup) {
    body += `## Subcommands\n\n| Command | Description |\n| --- | --- |\n`;
    for (const c of parsed.commands) {
      const slug = [...cmdPath, c.name].join('/');
      body += `| [\`${full} ${c.name}\`](/docs/reference/${slug}/) | ${tableEscape(c.short)} |\n`;
    }
    body += '\n';
  }

  const ownFlags = parsed.flags.filter((f) => !f.spec.includes('--help'));
  if (ownFlags.length > 0) body += `## Flags\n\n${flagsTable(ownFlags)}\n\n`;
  if (parsed.globalFlags.length > 0)
    body += `## Global flags\n\n${flagsTable(parsed.globalFlags)}\n\n`;

  const fm = [
    '---',
    `title: "${full}"`,
    `description: "${metaDescription(full, parsed.long).replace(/"/g, '\\"')}"`,
    'sidebar:',
    `  label: "${label}"`,
    ...(isGroup || cmdPath.length === 0 ? ['  order: 0'] : []),
    '---',
  ].join('\n');

  return `${fm}\n\n<!-- Generated by website/scripts/gen-reference.mjs — do not edit. -->\n\n${body.trimEnd()}\n`;
}

let pageCount = 0;
let leafCount = 0;

function walk(cmdPath) {
  const parsed = parseHelp(helpText(cmdPath));
  const isGroup = parsed.commands.length > 0;
  const file =
    cmdPath.length === 0
      ? join(OUT, 'index.md')
      : isGroup
        ? join(OUT, ...cmdPath, 'index.md')
        : join(OUT, ...cmdPath.slice(0, -1), `${cmdPath[cmdPath.length - 1]}.md`);
  mkdirSync(dirname(file), { recursive: true });
  writeFileSync(file, renderPage(cmdPath, parsed));
  pageCount += 1;
  if (!isGroup && cmdPath.length > 0) leafCount += 1;
  for (const c of parsed.commands) walk([...cmdPath, c.name]);
  return parsed;
}

ensureBinary();
rmSync(OUT, { recursive: true, force: true });
const root = walk([]);

const namespaces = root.commands.filter((c) => !['version'].includes(c.name));
mkdirSync(dirname(DATA_OUT), { recursive: true });
writeFileSync(
  DATA_OUT,
  JSON.stringify(
    {
      generatedFrom: 'gplay --help (scripts/gen-reference.mjs)',
      commands: leafCount,
      pages: pageCount,
      namespaces: namespaces.map((c) => ({ name: c.name, short: c.short })),
    },
    null,
    2,
  ) + '\n',
);

console.log(`gen-reference: wrote ${pageCount} pages (${leafCount} runnable commands) to ${OUT}`);
