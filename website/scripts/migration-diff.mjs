#!/usr/bin/env node

import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = resolve(__dirname, '../..');
const GOLDEN = 'cmd/gplay/testdata/surface.golden';
const DEFAULT_GUIDE = join(__dirname, '../src/content/docs/docs/migration/2-0.md');

const args = process.argv.slice(2);
let base = 'v1.6.2';
let check = null;
for (let i = 0; i < args.length; i++) {
  if (args[i] === '--base') base = args[++i];
  else if (args[i] === '--check') {
    check = args[i + 1] && !args[i + 1].startsWith('--') ? resolve(args[++i]) : DEFAULT_GUIDE;
  } else {
    console.error(`migration-diff: unknown argument ${args[i]}`);
    process.exit(2);
  }
}

let baseText;
try {
  baseText = execFileSync('git', ['show', `${base}:${GOLDEN}`], {
    cwd: REPO_ROOT,
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
  });
} catch {
  console.error(`migration-diff: cannot read ${GOLDEN} at ${base} (run \`git fetch --tags\`?)`);
  process.exit(2);
}
const headText = readFileSync(join(REPO_ROOT, GOLDEN), 'utf8');

function parse(text) {
  const out = [];
  for (const raw of text.split('\n')) {
    let m;
    if ((m = raw.match(/^(frozen|experimental) exit (\d+) /))) {
      out.push({ raw, stab: m[1], kind: 'exit', leaf: '', name: m[2] });
    } else if ((m = raw.match(/^(frozen|experimental) global (--[a-z0-9-]+)/))) {
      out.push({ raw, stab: m[1], kind: 'global', leaf: '', name: m[2] });
    } else if ((m = raw.match(/^(frozen|experimental) leaf "([^"]+)"/))) {
      out.push({ raw, stab: m[1], kind: 'leaf', leaf: m[2], name: '' });
    } else if ((m = raw.match(/^(frozen|experimental) flag "([^"]+)" (--[a-z0-9-]+)/))) {
      out.push({ raw, stab: m[1], kind: 'flag', leaf: m[2], name: m[3] });
    }
  }
  return out;
}

const before = parse(baseText);
const after = parse(headText);
const beforeRaw = new Set(before.map((r) => r.raw));
const afterRaw = new Set(after.map((r) => r.raw));
const removed = before.filter((r) => !afterRaw.has(r.raw));
const added = after.filter((r) => !beforeRaw.has(r.raw));

const key = (r) => `${r.kind} ${r.leaf} ${r.name}`;
const afterKeys = new Set(after.map(key));
const afterLeaves = new Set(after.filter((r) => r.kind === 'leaf').map((r) => r.leaf));

function closestLeaves(leaf) {
  const words = leaf.split(' ');
  const shared = (other) => {
    const o = other.split(' ');
    let n = 0;
    while (n < words.length && n < o.length && words[n] === o[n]) n++;
    return n;
  };
  const pool = added.filter((a) => a.kind === 'leaf').map((a) => a.leaf);
  const best = Math.max(0, ...pool.map(shared));
  return best === 0 ? [] : pool.filter((p) => shared(p) === best);
}

const findings = [];
for (const r of removed) {
  const modified = afterKeys.has(key(r));
  if (r.kind === 'exit') {
    findings.push({ r, what: modified ? 'reworded' : 'removed exit code', candidates: [] });
  } else if (r.kind === 'global') {
    findings.push({ r, what: modified ? 'modified global' : 'removed global', candidates: [] });
  } else if (r.kind === 'leaf') {
    const candidates = modified ? [r.leaf] : closestLeaves(r.leaf);
    findings.push({ r, what: modified ? 'modified leaf' : 'removed leaf', candidates });
  } else if (!afterLeaves.has(r.leaf)) {
    findings.push({ r, what: 'gone with its leaf', candidates: [] });
  } else {
    const candidates = modified
      ? [r.name]
      : added.filter((a) => a.kind === 'flag' && a.leaf === r.leaf).map((a) => a.name);
    findings.push({ r, what: modified ? 'modified flag' : 'removed flag', candidates });
  }
}

const escape = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
const token = (s) => new RegExp(`(^|[^A-Za-z0-9-])${escape(s)}($|[^A-Za-z0-9-])`);

const needsRow = (f) => ['removed leaf', 'removed flag', 'modified flag', 'removed global', 'removed exit code'].includes(f.what);

if (!check) {
  console.log(`Surface diff ${base} -> working tree (${GOLDEN})`);
  console.log(`${removed.length} lines removed or modified, ${added.length} added\n`);
  for (const f of findings) {
    const repl = f.candidates.length ? `  -> ${f.candidates.join(' | ')}` : '';
    const label = f.r.kind === 'exit' ? `exit ${f.r.name}` : `${f.r.leaf} ${f.r.name}`.trim();
    console.log(`[${f.r.stab}] ${f.what}: ${label}${repl}`);
  }
  process.exit(0);
}

const rows = readFileSync(check, 'utf8')
  .split('\n')
  .filter((l) => l.trimStart().startsWith('|'));

const missing = [];
let covered = 0;
for (const f of findings.filter(needsRow)) {
  const name = f.r.kind === 'exit' ? `exit ${f.r.name}` : f.r.name;
  const ok = rows.some(
    (row) =>
      (!f.r.leaf || token(f.r.leaf).test(row)) &&
      (!name || token(name).test(row)) &&
      (f.candidates.length === 0 || f.candidates.some((c) => token(c).test(row))),
  );
  if (ok) covered++;
  else missing.push(f);
}

const frozenCount = findings.filter((f) => needsRow(f) && f.r.stab === 'frozen').length;
if (missing.length) {
  console.error(`migration-diff: ${missing.length} removal(s) since ${base} missing from ${check}:`);
  for (const f of missing) {
    console.error(`  [${f.r.stab}] ${f.what}: ${f.r.raw}`);
    if (f.candidates.length) console.error(`      expected a table row naming one of: ${f.candidates.join(', ')}`);
  }
  process.exit(1);
}
console.log(
  `migration-diff: OK, all ${covered} removals since ${base} (${frozenCount} frozen) have a table row with their replacement.`,
);
