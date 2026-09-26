#!/usr/bin/env node
// Checks the built site (dist/) for the defects a build alone lets through:
//
//   1. Internal links: every root-absolute, relative, or same-origin href/src
//      on every HTML page must resolve to a built file, and a #fragment must
//      name an id on the target page. The Worker serves dist/ with
//      html_handling "auto-trailing-slash", so /a, /a/ and /a.html all count.
//      External links are not fetched: the check stays offline.
//   2. Repo links: a github.com/PollyGlot/google-play-cli/{edit,blob,tree}/main
//      link must point at a tracked path. Starlight's "Edit page" link on a
//      page generated into a gitignored directory 404s on GitHub; this is
//      what catches it.
//   3. Flag dashes: no page may show a flag whose `--` became an en or em dash
//      (smart punctuation turning `--track` into a dash glyph plus "track"
//      corrupts every copy-pasted command). astro.config.mjs turns smartypants off; this
//      proves it stayed off.
//
// Usage: node scripts/check-dist.mjs [dist-dir]   (after `npm run build`)
// Honours SITE_URL and SITE_BASE like astro.config.mjs. Exits 1 on any finding.

import { execFileSync } from 'node:child_process';
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import { dirname, join, relative, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = resolve(__dirname, '../..');
const DIST = resolve(process.argv[2] ?? join(__dirname, '../dist'));
const SITE = new URL(process.env.SITE_URL ?? 'https://gplay.sh');
const BASE = (process.env.SITE_BASE ?? '/').replace(/\/$/, '');
const REPO_LINK = /^https:\/\/github\.com\/PollyGlot\/google-play-cli\/(edit|blob|tree)\/main\/([^?#]*)/;
// An en or em dash (U+2013, U+2014) glued to a flag-like word at a word start.
// A prose range such as 1 to 2 written with an en dash has a character before
// the dash and never matches.
const MANGLED_FLAG = /(^|[\s(>`"'[])([\u2013\u2014][a-z][a-z0-9]*(?:-[a-z0-9]+)*)/gm;

if (!existsSync(DIST)) {
  console.error(`check-dist: ${DIST} not found, run \`npm run build\` first`);
  process.exit(2);
}

function walk(dir) {
  const out = [];
  for (const name of readdirSync(dir)) {
    const full = join(dir, name);
    if (statSync(full).isDirectory()) out.push(...walk(full));
    else out.push(full);
  }
  return out;
}

const files = walk(DIST);
const htmlFiles = files.filter((f) => f.endsWith('.html'));
const textFiles = files.filter((f) => /\.(html|md|txt)$/.test(f));

const decode = (s) =>
  s
    .replace(/&amp;/g, '&')
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>');

// URL path (as the Worker sees it) of a built HTML file.
function pageUrl(file) {
  const rel = relative(DIST, file).split(sep).join('/');
  if (rel === 'index.html') return `${BASE}/`;
  if (rel.endsWith('/index.html')) return `${BASE}/${rel.slice(0, -'index.html'.length)}`;
  return `${BASE}/${rel}`;
}

// The built file a URL path is served from, or null.
function resolvePath(pathname) {
  if (BASE && pathname !== BASE && !pathname.startsWith(`${BASE}/`)) return null;
  let p;
  try {
    p = decodeURIComponent(pathname.slice(BASE.length));
  } catch {
    return null;
  }
  const local = join(DIST, ...p.split('/'));
  if (!local.startsWith(DIST)) return null;
  // auto-trailing-slash redirects between /a/ and /a whichever file exists.
  const bare = local.replace(/[\\/]+$/, '');
  const candidates = [local, join(bare, 'index.html'), `${bare}.html`];
  return candidates.find((c) => existsSync(c) && statSync(c).isFile()) ?? null;
}

const idCache = new Map();
function idsOf(file) {
  if (!idCache.has(file)) {
    const html = readFileSync(file, 'utf8');
    const ids = new Set();
    for (const m of html.matchAll(/\s(?:id|name)="([^"]+)"/g)) ids.add(decode(m[1]));
    idCache.set(file, ids);
  }
  return idCache.get(file);
}

let tracked;
function isTracked(path, isDir) {
  tracked ??= new Set(
    execFileSync('git', ['ls-files', '-z'], { cwd: REPO_ROOT, encoding: 'utf8' })
      .split('\0')
      .filter(Boolean),
  );
  const clean = path.replace(/\/$/, '');
  if (!isDir) return tracked.has(clean);
  for (const f of tracked) if (f.startsWith(`${clean}/`)) return true;
  return false;
}

const broken = [];
const deadRepoLinks = [];
let checked = 0;

for (const file of htmlFiles) {
  const html = readFileSync(file, 'utf8');
  const here = new URL(pageUrl(file), SITE);
  const page = relative(DIST, file);
  for (const m of html.matchAll(/\s(?:href|src)="([^"]*)"/g)) {
    const raw = decode(m[1]);
    const repo = raw.match(REPO_LINK);
    if (repo) {
      let path;
      try {
        path = decodeURIComponent(repo[2]);
      } catch {
        path = repo[2];
      }
      if (!isTracked(path, repo[1] === 'tree')) deadRepoLinks.push({ page, href: raw });
      continue;
    }
    if (raw === '' || raw.startsWith('//')) continue;
    let target;
    try {
      target = new URL(raw, here);
    } catch {
      broken.push({ page, href: raw });
      continue;
    }
    if (target.origin !== SITE.origin) continue; // external (or mailto:, data:, ...)
    checked += 1;
    const resolved = resolvePath(target.pathname);
    if (!resolved) {
      broken.push({ page, href: raw });
      continue;
    }
    let fragment;
    try {
      fragment = decodeURIComponent(target.hash.slice(1));
    } catch {
      fragment = target.hash.slice(1);
    }
    // Starlight's "#_top" skip link targets the document itself.
    if (fragment && fragment !== '_top' && resolved.endsWith('.html')) {
      if (!idsOf(resolved).has(fragment)) broken.push({ page, href: raw });
    }
  }
}

const mangled = [];
for (const file of textFiles) {
  const text = readFileSync(file, 'utf8');
  for (const m of text.matchAll(MANGLED_FLAG)) {
    mangled.push({ page: relative(DIST, file), text: m[2] });
  }
}

function report(title, items, fmt) {
  if (items.length === 0) return;
  console.error(`\ncheck-dist: ${items.length} ${title}`);
  for (const item of items.slice(0, 50)) console.error(`  ${fmt(item)}`);
  if (items.length > 50) console.error(`  ... and ${items.length - 50} more`);
}

report('broken internal link(s)', broken, (b) => `${b.page}: ${b.href}`);
report('link(s) to an untracked repo path', deadRepoLinks, (b) => `${b.page}: ${b.href}`);
report('flag(s) with a mangled dash', mangled, (b) => `${b.page}: ${b.text}`);

const failed = broken.length + deadRepoLinks.length + mangled.length;
console.log(
  `check-dist: ${htmlFiles.length} pages, ${checked} internal links, ` +
    `${failed === 0 ? 'no findings' : `${failed} finding(s)`}`,
);
process.exit(failed === 0 ? 0 : 1);
