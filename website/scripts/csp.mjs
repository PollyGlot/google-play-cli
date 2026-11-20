import { createHash } from 'node:crypto';
import { readdir, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const EXECUTABLE_TYPES = new Set(['', 'module', 'text/javascript', 'application/javascript']);

const SCRIPT_TAG = /<script\b([^>]*)>([\s\S]*?)<\/script\s*>/gi;
const HEAD_OPEN = /<head\b[^>]*>/i;
const CHARSET_META = /<meta\s+charset=["']?[\w-]+["']?\s*\/?>/i;

const BASE_DIRECTIVES = [
  "default-src 'self'",
  "style-src 'self' 'unsafe-inline'",
  "img-src 'self' data:",
  "font-src 'self'",
  "connect-src 'self' https://cloudflareinsights.com",
  "object-src 'none'",
  "base-uri 'self'",
  "form-action 'self'",
];

const SCRIPT_SOURCES = ["'self'", "'wasm-unsafe-eval'", 'https://static.cloudflareinsights.com'];

export function scriptHash(body) {
  return `'sha256-${createHash('sha256').update(body, 'utf8').digest('base64')}'`;
}

export function buildPolicy(hashes) {
  const scriptSrc = ['script-src', ...SCRIPT_SOURCES, ...hashes].join(' ');
  return [...BASE_DIRECTIVES, scriptSrc].join('; ');
}

export function stampCsp(html, file = 'page') {
  if (/http-equiv=["']?content-security-policy/i.test(html)) {
    throw new Error(`csp: ${file} already carries a CSP meta`);
  }
  const hashes = new Set();
  for (const [, attrs, body] of html.matchAll(SCRIPT_TAG)) {
    if (/\bsrc\s*=/i.test(attrs)) continue;
    const type = (attrs.match(/\btype\s*=\s*["']?([^"'\s>]+)/i)?.[1] ?? '').toLowerCase();
    if (!EXECUTABLE_TYPES.has(type)) continue;
    hashes.add(scriptHash(body));
  }
  const meta = `<meta http-equiv="content-security-policy" content="${buildPolicy([...hashes])}">`;
  const anchor = html.match(CHARSET_META) ?? html.match(HEAD_OPEN);
  if (!anchor) throw new Error(`csp: ${file} has no <head>`);
  const at = anchor.index + anchor[0].length;
  return html.slice(0, at) + meta + html.slice(at);
}

async function* htmlFiles(dir) {
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) yield* htmlFiles(path);
    else if (entry.name.endsWith('.html')) yield path;
  }
}

export default function cspMeta() {
  return {
    name: 'gplay-csp-meta',
    hooks: {
      'astro:build:done': async ({ dir, logger }) => {
        let pages = 0;
        for await (const file of htmlFiles(fileURLToPath(dir))) {
          await writeFile(file, stampCsp(await readFile(file, 'utf8'), file));
          pages++;
        }
        logger.info(`CSP meta stamped into ${pages} pages`);
      },
    },
  };
}
