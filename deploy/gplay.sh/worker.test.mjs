// Routing tests for worker.js, driven through its real `fetch` entry point
// with a fake static-asset binding (env.ASSETS) and a stubbed global fetch for
// the /install upstream: no network, no wrangler, no Cloudflare runtime.
//
//   node --test deploy/gplay.sh/worker.test.mjs
//
// Going through the default export rather than exporting internals keeps the
// deployed module's shape untouched and tests what a request actually gets,
// security headers included.

import assert from 'node:assert/strict';
import { afterEach, beforeEach, describe, it, mock } from 'node:test';

import worker from './worker.js';

const RAW_INSTALL_URL =
  'https://raw.githubusercontent.com/PollyGlot/google-play-cli/main/install.sh';

const SECURITY_HEADERS = [
  'x-content-type-options',
  'x-frame-options',
  'referrer-policy',
  'permissions-policy',
  'strict-transport-security',
];

// A static-asset binding serving a fixed map of path -> response init.
// Records every request so tests can assert which twins were looked up.
function fakeAssets(files) {
  const requests = [];
  return {
    requests,
    async fetch(request) {
      const url = new URL(request.url);
      requests.push({ path: url.pathname, headers: request.headers });
      const file = files[url.pathname];
      if (!file) {
        return new Response('not found', {
          status: 404,
          headers: { 'content-type': 'text/html; charset=utf-8' },
        });
      }
      if (typeof file === 'function') return file(request);
      return new Response(file.body ?? null, {
        status: file.status ?? 200,
        headers: file.headers ?? {},
      });
    },
  };
}

const SITE_FILES = {
  '/docs/quickstart/': {
    body: '<h1>Quickstart</h1>',
    headers: { 'content-type': 'text/html; charset=utf-8', etag: '"html"' },
  },
  '/docs/quickstart.md': {
    body: '# Quickstart',
    headers: { 'content-type': 'application/octet-stream', etag: '"md"' },
  },
  '/docs/': { body: '<h1>Docs</h1>', headers: { 'content-type': 'text/html' } },
  '/docs/index.md': { body: '# Docs', headers: { 'content-type': 'text/plain' } },
  '/': { body: '<h1>gplay</h1>', headers: { 'content-type': 'text/html' } },
  '/llms.txt': { body: '# gplay llms', headers: { 'content-type': 'text/plain' } },
  '/no-twin/': { body: '<p>html only</p>', headers: { 'content-type': 'text/html' } },
  '/_astro/app.abc123.js': {
    body: 'console.log(1)',
    headers: { 'content-type': 'text/javascript', 'cache-control': 'max-age=0' },
  },
  '/favicon.svg': { body: '<svg/>', headers: { 'content-type': 'image/svg+xml' } },
};

let env;
beforeEach(() => {
  env = { ASSETS: fakeAssets(SITE_FILES) };
});
afterEach(() => mock.restoreAll());

function get(url, headers = {}, method = 'GET') {
  return worker.fetch(new Request(url, { method, headers }), env);
}

function assertHardened(res) {
  for (const h of SECURITY_HEADERS) assert.ok(res.headers.get(h), `missing ${h}`);
}

describe('hostname canonicalisation', () => {
  const cases = [
    ['https://docs.gplay.sh/', 'https://gplay.sh/docs'],
    ['https://docs.gplay.sh/quickstart', 'https://gplay.sh/docs/quickstart'],
    ['https://docs.gplay.sh/docs', 'https://gplay.sh/docs'],
    ['https://docs.gplay.sh/docs/guides/ci-cd/', 'https://gplay.sh/docs/guides/ci-cd/'],
    // A lookalike is not the canonical /docs prefix and must still get it.
    ['https://docs.gplay.sh/docs2', 'https://gplay.sh/docs/docs2'],
    ['https://docs.gplay.sh/docsy/page', 'https://gplay.sh/docs/docsy/page'],
    ['https://docs.gplay.sh/reference?x=1#top', 'https://gplay.sh/docs/reference?x=1#top'],
    ['https://www.gplay.sh/', 'https://gplay.sh/'],
    ['https://www.gplay.sh/docs/quickstart/', 'https://gplay.sh/docs/quickstart/'],
    ['https://www.gplay.sh/install', 'https://gplay.sh/install'],
  ];
  for (const [from, to] of cases) {
    it(`${from} -> 301 ${to}`, async () => {
      const res = await get(from);
      assert.equal(res.status, 301);
      assert.equal(res.headers.get('location'), to);
      assertHardened(res);
      assert.equal(env.ASSETS.requests.length, 0);
    });
  }

  it('leaves the apex alone', async () => {
    const res = await get('https://gplay.sh/docs/quickstart/');
    assert.equal(res.status, 200);
    assert.equal(await res.text(), '<h1>Quickstart</h1>');
  });
});

describe('/install proxy', () => {
  let logs;
  beforeEach(() => {
    logs = [];
    mock.method(console, 'log', (entry) => logs.push(entry));
  });

  for (const path of ['/install', '/install.sh']) {
    it(`GET ${path} proxies install.sh from main as text/plain`, async () => {
      const upstream = mock.method(globalThis, 'fetch', async () =>
        new Response('#!/bin/sh\necho hi\n', { status: 200 }),
      );
      const res = await get(`https://gplay.sh${path}`, { 'user-agent': 'curl/8.7.1' });
      assert.equal(res.status, 200);
      assert.equal(res.headers.get('content-type'), 'text/plain; charset=utf-8');
      assert.equal(res.headers.get('cache-control'), 'public, max-age=300');
      assert.equal(await res.text(), '#!/bin/sh\necho hi\n');
      assertHardened(res);

      assert.equal(upstream.mock.callCount(), 1);
      const [url, init] = upstream.mock.calls[0].arguments;
      assert.equal(url, RAW_INSTALL_URL);
      assert.equal(init.method, 'GET');
      assert.equal(env.ASSETS.requests.length, 0);
    });
  }

  it('forwards HEAD upstream', async () => {
    const upstream = mock.method(globalThis, 'fetch', async () => new Response(null));
    const res = await get('https://gplay.sh/install', {}, 'HEAD');
    assert.equal(res.status, 200);
    assert.equal(upstream.mock.calls[0].arguments[1].method, 'HEAD');
  });

  it('logs an install_hit without the client IP', async () => {
    mock.method(globalThis, 'fetch', async () => new Response('x'));
    await get('https://gplay.sh/install', {
      'user-agent': 'curl/8.7.1',
      'cf-connecting-ip': '203.0.113.9',
    });
    assert.equal(logs.length, 1);
    assert.equal(logs[0].event, 'install_hit');
    assert.equal(logs[0].userAgent, 'curl/8.7.1');
    assert.ok(!JSON.stringify(logs[0]).includes('203.0.113.9'));
  });

  for (const method of ['POST', 'PUT', 'DELETE']) {
    it(`${method} /install -> 405 with Allow`, async () => {
      const upstream = mock.method(globalThis, 'fetch', async () => new Response('x'));
      const res = await get('https://gplay.sh/install', {}, method);
      assert.equal(res.status, 405);
      assert.equal(res.headers.get('allow'), 'GET, HEAD');
      assertHardened(res);
      assert.equal(upstream.mock.callCount(), 0);
    });
  }

  it('upstream failure -> 502 naming the upstream status', async () => {
    mock.method(globalThis, 'fetch', async () => new Response('oops', { status: 503 }));
    const res = await get('https://gplay.sh/install');
    assert.equal(res.status, 502);
    assert.match(await res.text(), /upstream 503/);
    assertHardened(res);
  });
});

describe('Markdown negotiation', () => {
  const markdownAccepts = [
    'text/markdown',
    'text/x-markdown',
    'TEXT/MARKDOWN',
    'text/html, text/markdown', // tie: Markdown wins
    'text/html;q=0.5, text/markdown',
    'text/markdown;q=0.8, text/html;q=0.8',
    'text/markdown; charset=utf-8; q=0.9, text/html;q=0.1',
    'text/html;Q=0.2, text/markdown;Q=0.3',
  ];
  for (const accept of markdownAccepts) {
    it(`Accept "${accept}" serves the Markdown twin`, async () => {
      const res = await get('https://gplay.sh/docs/quickstart/', { accept });
      assert.equal(res.status, 200);
      assert.equal(res.headers.get('content-type'), 'text/markdown; charset=utf-8');
      assert.equal(await res.text(), '# Quickstart');
    });
  }

  const htmlAccepts = [
    undefined,
    '',
    '*/*',
    'text/html',
    'text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8',
    'text/html, text/markdown;q=0.9',
    'text/markdown;q=0',
    'text/markdown;q=0, text/html;q=0',
    'text/plain, application/json',
  ];
  for (const accept of htmlAccepts) {
    it(`Accept ${JSON.stringify(accept)} serves HTML`, async () => {
      const headers = accept === undefined ? {} : { accept };
      const res = await get('https://gplay.sh/docs/quickstart/', headers);
      assert.equal(res.status, 200);
      assert.equal(await res.text(), '<h1>Quickstart</h1>');
      assert.ok(!env.ASSETS.requests.some((r) => r.path.endsWith('.md')));
    });
  }

  it('decorates the Markdown reply for caches and agents', async () => {
    const res = await get('https://gplay.sh/docs/quickstart/', { accept: 'text/markdown' });
    assert.equal(res.headers.get('vary'), 'Accept');
    assert.equal(res.headers.get('cache-control'), 'public, max-age=300');
    assert.match(res.headers.get('link'), /rel="service-doc"/);
    assert.match(res.headers.get('link'), /rel="describedby"/);
    assert.equal(res.headers.get('etag'), '"md"'); // asset validators survive
    assertHardened(res);
  });

  it('resolves leaf pages with and without a trailing slash or .html', async () => {
    for (const path of ['/docs/quickstart', '/docs/quickstart/', '/docs/quickstart.html']) {
      env = { ASSETS: fakeAssets(SITE_FILES) };
      const res = await get(`https://gplay.sh${path}`, { accept: 'text/markdown' });
      assert.equal(await res.text(), '# Quickstart', path);
      assert.equal(env.ASSETS.requests[0].path, '/docs/quickstart.md');
    }
  });

  it('falls back to <path>/index.md for index pages', async () => {
    const res = await get('https://gplay.sh/docs/', { accept: 'text/markdown' });
    assert.equal(await res.text(), '# Docs');
    assert.deepEqual(
      env.ASSETS.requests.map((r) => r.path),
      ['/docs.md', '/docs/index.md'],
    );
  });

  it('maps the landing page to llms.txt', async () => {
    for (const path of ['/', '/index.html']) {
      env = { ASSETS: fakeAssets(SITE_FILES) };
      const res = await get(`https://gplay.sh${path}`, { accept: 'text/markdown' });
      assert.equal(res.headers.get('content-type'), 'text/markdown; charset=utf-8');
      assert.equal(await res.text(), '# gplay llms');
    }
  });

  it('drops the query string when looking up the twin', async () => {
    const res = await get('https://gplay.sh/docs/quickstart/?ref=x', { accept: 'text/markdown' });
    assert.equal(await res.text(), '# Quickstart');
  });

  it('falls through to HTML when no twin exists', async () => {
    const res = await get('https://gplay.sh/no-twin/', { accept: 'text/markdown' });
    assert.equal(res.status, 200);
    assert.equal(await res.text(), '<p>html only</p>');
    assert.deepEqual(
      env.ASSETS.requests.map((r) => r.path),
      ['/no-twin.md', '/no-twin/index.md', '/no-twin/'],
    );
  });

  it('only negotiates on GET', async () => {
    const res = await get('https://gplay.sh/docs/quickstart/', { accept: 'text/markdown' }, 'HEAD');
    assert.equal(res.headers.get('content-type'), 'text/html; charset=utf-8');
    assert.ok(!env.ASSETS.requests.some((r) => r.path.endsWith('.md')));
  });

  it('passes a 304 through with the conditional headers forwarded', async () => {
    env = {
      ASSETS: fakeAssets({
        ...SITE_FILES,
        '/docs/quickstart.md': (req) =>
          req.headers.get('if-none-match') === '"md"'
            ? new Response(null, {
                status: 304,
                headers: { etag: '"md"', 'content-type': 'application/octet-stream' },
              })
            : new Response('# Quickstart', { status: 200 }),
      }),
    };
    const res = await get('https://gplay.sh/docs/quickstart/', {
      accept: 'text/markdown',
      'if-none-match': '"md"',
      'if-modified-since': 'Wed, 01 Jan 2026 00:00:00 GMT',
    });
    assert.equal(res.status, 304);
    assert.equal(res.body, null);
    // A 304 echoes the original representation, so content-type is not rewritten.
    assert.equal(res.headers.get('content-type'), 'application/octet-stream');
    assert.equal(res.headers.get('vary'), 'Accept');
    const forwarded = env.ASSETS.requests[0].headers;
    assert.equal(forwarded.get('if-none-match'), '"md"');
    assert.equal(forwarded.get('if-modified-since'), 'Wed, 01 Jan 2026 00:00:00 GMT');
    assert.equal(forwarded.get('accept'), null); // only the validators are forwarded
  });
});

describe('static assets', () => {
  it('tags HTML with the discovery Link header and Vary: Accept', async () => {
    const res = await get('https://gplay.sh/docs/quickstart/');
    assert.match(res.headers.get('link'), /agent-skills\/index\.json/);
    assert.equal(res.headers.get('vary'), 'Accept');
    assertHardened(res);
  });

  it('appends to an existing Vary and Link rather than replacing them', async () => {
    env = {
      ASSETS: fakeAssets({
        '/p/': {
          body: 'x',
          headers: {
            'content-type': 'text/html',
            vary: 'Accept-Encoding',
            link: '</a.css>; rel="preload"',
          },
        },
      }),
    };
    const res = await get('https://gplay.sh/p/');
    assert.equal(res.headers.get('vary'), 'Accept-Encoding, Accept');
    assert.match(res.headers.get('link'), /^<\/a\.css>; rel="preload", <\/docs\/>/);
  });

  it('leaves non-HTML assets undecorated', async () => {
    const res = await get('https://gplay.sh/favicon.svg');
    assert.equal(res.headers.get('link'), null);
    assert.equal(res.headers.get('vary'), null);
    assertHardened(res);
  });

  it('marks content-hashed /_astro/ assets immutable', async () => {
    const res = await get('https://gplay.sh/_astro/app.abc123.js');
    assert.equal(res.headers.get('cache-control'), 'public, max-age=31536000, immutable');
  });

  it('does not mark a missing /_astro/ asset immutable', async () => {
    const res = await get('https://gplay.sh/_astro/gone.js');
    assert.equal(res.status, 404);
    assert.notEqual(res.headers.get('cache-control'), 'public, max-age=31536000, immutable');
  });

  it('passes the 404 page through', async () => {
    const res = await get('https://gplay.sh/nope');
    assert.equal(res.status, 404);
    assertHardened(res);
  });
});

describe('failure isolation', () => {
  it('degrades to plain asset serving when routing throws', async () => {
    let calls = 0;
    env = {
      ASSETS: {
        async fetch() {
          calls += 1;
          if (calls === 1) throw new Error('boom');
          return new Response('fallback', { headers: { 'content-type': 'text/html' } });
        },
      },
    };
    const res = await get('https://gplay.sh/docs/quickstart/');
    assert.equal(res.status, 200);
    assert.equal(await res.text(), 'fallback');
    assertHardened(res);
    // The fallback skips decoration: it is plain asset serving.
    assert.equal(res.headers.get('link'), null);
  });
});
