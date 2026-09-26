// Astro integration: stamp a hash-based Content-Security-Policy <meta> into
// every built HTML page (issue #600, SITE-05).
//
// Why not Astro's own `security.csp`: tried first, it breaks this site twice.
// It never hashes `is:inline` scripts, and Starlight's first-paint theme,
// search and sidebar scripts are all `is:inline`, so they were blocked. And
// its style-src always carries hashes, which makes browsers ignore
// 'unsafe-inline', so the thousands of style="" attributes Starlight and
// Expressive Code emit (icon sizes, sidebar depth, syntax colours) were
// blocked too. Hashing the finished HTML covers every inline script whoever
// emitted it, and leaves style-src free of hashes.
//
// The policy ships as a <meta>, so the page carries it wherever it is served,
// and a meta cannot express frame-ancestors or reporting: the Worker's
// x-frame-options: DENY keeps covering framing (deploy/gplay.sh/worker.js).

import { createHash } from 'node:crypto';
import { readdir, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

// Script types the browser executes. Anything else (JSON-LD) is a data block
// that CSP does not govern, so hashing it would only bloat the policy.
const EXECUTABLE_TYPES = new Set(['', 'module', 'text/javascript', 'application/javascript']);

const SCRIPT_TAG = /<script\b([^>]*)>([\s\S]*?)<\/script\s*>/gi;
const HEAD_OPEN = /<head\b[^>]*>/i;
const CHARSET_META = /<meta\s+charset=["']?[\w-]+["']?\s*\/?>/i;

/** Directives other than script-src, in emission order. */
const BASE_DIRECTIVES = [
  "default-src 'self'",
  // Styles stay 'unsafe-inline' on purpose (see the header): the XSS
  // boundary is script-src, and style attributes are how Starlight sizes
  // icons and colours code.
  "style-src 'self' 'unsafe-inline'",
  // data: covers the SVG data URIs in Starlight's and Pagefind's CSS.
  "img-src 'self' data:",
  "font-src 'self'",
  // Cloudflare Web Analytics posts its beacon here (kept, #600 decision).
  "connect-src 'self' https://cloudflareinsights.com",
  "object-src 'none'",
  "base-uri 'self'",
  "form-action 'self'",
];

// 'wasm-unsafe-eval': Pagefind (Starlight's search) compiles its WebAssembly
// index in the page when its worker is unavailable. static.cloudflareinsights
// .com: the Web Analytics beacon Cloudflare injects at the edge, after build.
const SCRIPT_SOURCES = ["'self'", "'wasm-unsafe-eval'", 'https://static.cloudflareinsights.com'];

/** SHA-256 CSP source for an inline script body, hashed exactly as written. */
export function scriptHash(body) {
  return `'sha256-${createHash('sha256').update(body, 'utf8').digest('base64')}'`;
}

/** The full policy for a page whose inline scripts hash to `hashes`. */
export function buildPolicy(hashes) {
  const scriptSrc = ['script-src', ...SCRIPT_SOURCES, ...hashes].join(' ');
  return [...BASE_DIRECTIVES, scriptSrc].join('; ');
}

/** Return `html` with its CSP meta inserted as the first child of <head>. */
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
  // A meta policy only governs what the parser meets after it, so it must
  // precede the first-paint theme scripts at the top of <head>. It goes after
  // <meta charset> when there is one: the policy is ~1 KB with its hashes,
  // and the charset must sit in the first 1024 bytes (the Worker serves
  // text/html without a charset parameter).
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
