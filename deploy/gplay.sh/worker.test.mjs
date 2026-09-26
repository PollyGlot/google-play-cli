// Offline tests for the /install route of worker.js (issue #601, ADR-0009
// amendment). Run with `node --test deploy/gplay.sh/` (also `make worker-test`
// and the "Docs sanity" CI job). globalThis.fetch is replaced by a fake for
// every test, so nothing here reaches GitHub or Cloudflare.

import { test, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";

import worker from "./worker.js";

const API = "https://api.github.com/repos/PollyGlot/google-play-cli/releases/latest";
const PAGE = "https://github.com/PollyGlot/google-play-cli/releases/latest";
const raw = (tag) =>
  `https://raw.githubusercontent.com/PollyGlot/google-play-cli/${tag}/install.sh`;
const SCRIPT = "#!/usr/bin/env sh\necho installer\n";

const env = {
  ASSETS: { fetch: async () => new Response("asset", { status: 404 }) },
};

const realFetch = globalThis.fetch;
let calls;
let warnings;

// Installs a fake fetch that answers from `routes` (URL -> handler) and records
// every call. An unrouted URL is a test bug, so it throws loudly.
function fakeFetch(routes) {
  globalThis.fetch = async (input, init = {}) => {
    const url = typeof input === "string" ? input : input.url;
    calls.push({ url, init });
    const handler = routes[url];
    if (!handler) throw new Error(`unexpected fetch ${url}`);
    return handler(init);
  };
}

const apiTag = (tag) => () =>
  new Response(JSON.stringify({ tag_name: tag }), {
    status: 200,
    headers: { "content-type": "application/json" },
  });
const pageRedirect = (tag) => () =>
  new Response(null, {
    status: 302,
    headers: { location: `https://github.com/PollyGlot/google-play-cli/releases/tag/${tag}` },
  });
const status = (code) => () => new Response("nope", { status: code });
const script = () => new Response(SCRIPT, { status: 200 });

function install(method = "GET", path = "/install") {
  return worker.fetch(new Request(`https://gplay.sh${path}`, { method }), env);
}

beforeEach((t) => {
  calls = [];
  warnings = [];
  t.mock.method(console, "log", () => {});
  t.mock.method(console, "warn", (entry) => warnings.push(entry));
});

afterEach(() => {
  globalThis.fetch = realFetch;
  // The whole point of #601: no path of the install route may read `main`.
  for (const { url } of calls) {
    assert.ok(!url.includes("/main/"), `fetched a branch URL: ${url}`);
  }
});

test("serves install.sh at the tag the releases API reports", async () => {
  fakeFetch({ [API]: apiTag("v1.6.2"), [raw("v1.6.2")]: script });
  const res = await install();
  assert.equal(res.status, 200);
  assert.equal(await res.text(), SCRIPT);
  assert.equal(res.headers.get("x-gplay-installer-ref"), "v1.6.2");
  assert.equal(res.headers.get("content-type"), "text/plain; charset=utf-8");
  assert.equal(res.headers.get("cache-control"), "public, max-age=300");
  assert.deepEqual(
    calls.map((c) => c.url),
    [API, raw("v1.6.2")],
  );
  assert.deepEqual(warnings, [], "the nominal path must not log a fallback");
});

test("/install.sh is the same route", async () => {
  fakeFetch({ [API]: apiTag("v1.6.2"), [raw("v1.6.2")]: script });
  const res = await install("GET", "/install.sh");
  assert.equal(res.status, 200);
  assert.equal(res.headers.get("x-gplay-installer-ref"), "v1.6.2");
});

test("the resolution is edge-cached briefly and never caches an error", async () => {
  fakeFetch({ [API]: apiTag("v1.6.2"), [raw("v1.6.2")]: script });
  await install();
  const { init } = calls[0];
  assert.equal(init.headers["user-agent"], "gplay-site-worker");
  assert.deepEqual(init.cf, {
    cacheEverything: true,
    cacheTtlByStatus: { "200-399": 300, "400-599": 0 },
  });
});

for (const [name, apiHandler] of [
  ["a rate-limited API (403)", status(403)],
  ["an API outage (500)", status(500)],
  ["a network error", () => Promise.reject(new TypeError("fetch failed"))],
  ["a non-JSON body", () => new Response("<html>", { status: 200 })],
  ["a branch name instead of a tag", apiTag("main")],
  ["a tag that would escape the URL path", apiTag("v1.0.0/../../main")],
]) {
  test(`falls back to the release page redirect on ${name}`, async () => {
    fakeFetch({ [API]: apiHandler, [PAGE]: pageRedirect("v1.6.1"), [raw("v1.6.1")]: script });
    const res = await install();
    assert.equal(res.status, 200);
    assert.equal(res.headers.get("x-gplay-installer-ref"), "v1.6.1");
    const page = calls.find((c) => c.url === PAGE);
    assert.equal(page.init.redirect, "manual", "the redirect must be read, not followed");
    assert.equal(warnings.length, 1, "a fallback is logged, never silent");
    assert.equal(warnings[0].event, "install_ref_fallback");
    assert.equal(warnings[0].source, "release-page");
  });
}

test("fails closed with 503 when neither lookup yields a tag", async () => {
  fakeFetch({
    [API]: status(403),
    [PAGE]: () =>
      new Response(null, { status: 302, headers: { location: "https://github.com/login" } }),
  });
  const res = await install();
  assert.equal(res.status, 503);
  assert.equal(res.headers.get("cache-control"), "no-store");
  assert.equal(res.headers.get("retry-after"), "60");
  assert.match(await res.text(), /refusing to serve an unreleased installer/);
  assert.ok(
    !calls.some((c) => c.url.startsWith("https://raw.githubusercontent.com/")),
    "no script is fetched without a resolved tag",
  );
  assert.equal(warnings[0].event, "install_unresolved");
  assert.equal(warnings[0].errors.length, 2);
});

test("fails closed when both lookups throw", async () => {
  const boom = () => Promise.reject(new TypeError("fetch failed"));
  fakeFetch({ [API]: boom, [PAGE]: boom });
  const res = await install();
  assert.equal(res.status, 503);
});

test("returns 502 when the tagged script cannot be fetched", async () => {
  fakeFetch({ [API]: apiTag("v1.6.2"), [raw("v1.6.2")]: status(404) });
  const res = await install();
  assert.equal(res.status, 502);
  assert.match(await res.text(), /v1\.6\.2 installer \(upstream 404\)/);
});

test("HEAD is forwarded as HEAD", async () => {
  fakeFetch({ [API]: apiTag("v1.6.2"), [raw("v1.6.2")]: () => new Response(null, { status: 200 }) });
  const res = await install("HEAD");
  assert.equal(res.status, 200);
  assert.equal(calls[1].init.method, "HEAD");
});

test("other methods are refused before any lookup", async () => {
  fakeFetch({});
  const res = await install("POST");
  assert.equal(res.status, 405);
  assert.equal(res.headers.get("allow"), "GET, HEAD");
  assert.equal(calls.length, 0);
});

test("security headers still wrap the install response", async () => {
  fakeFetch({ [API]: apiTag("v1.6.2"), [raw("v1.6.2")]: script });
  const res = await install();
  assert.equal(res.headers.get("x-content-type-options"), "nosniff");
  assert.equal(res.headers.get("x-frame-options"), "DENY");
});
