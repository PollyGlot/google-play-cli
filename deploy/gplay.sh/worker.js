// Cloudflare Worker for gplay.sh — serves the gplay install script.
//
//   GET /install      -> proxies install.sh from the repo's `main` branch
//                        as text/plain (the `curl … | sh` entry point)
//   GET /  (or empty)  -> 302 to the GitHub repo
//   anything else      -> 404
//
// The script is proxied from `main` (not a tag) on purpose: install.sh
// resolves the latest release itself, so `main` always serves the newest
// installer with the repo as the single source of truth (no static copy to
// drift). See docs/adr/0009-install-distribution-vanity-domain.md.

const RAW_INSTALL_URL =
  "https://raw.githubusercontent.com/PollyGlot/google-play-cli/main/install.sh";
const REPO_URL = "https://github.com/PollyGlot/google-play-cli";
const CACHE_SECONDS = 300;

export default {
  async fetch(request) {
    const { pathname } = new URL(request.url);

    if (request.method !== "GET" && request.method !== "HEAD") {
      return new Response("method not allowed\n", {
        status: 405,
        headers: {
          allow: "GET, HEAD",
          "content-type": "text/plain; charset=utf-8",
        },
      });
    }

    if (pathname === "/install" || pathname === "/install.sh") {
      const upstream = await fetch(RAW_INSTALL_URL, {
        method: request.method,
        cf: { cacheTtl: CACHE_SECONDS, cacheEverything: true },
      });
      if (!upstream.ok) {
        return new Response(
          `could not fetch installer (upstream ${upstream.status}). See ${REPO_URL}\n`,
          {
            status: 502,
            headers: { "content-type": "text/plain; charset=utf-8" },
          },
        );
      }
      return new Response(upstream.body, {
        status: 200,
        headers: {
          "content-type": "text/plain; charset=utf-8",
          "cache-control": `public, max-age=${CACHE_SECONDS}`,
          "x-content-type-options": "nosniff",
        },
      });
    }

    if (pathname === "/" || pathname === "") {
      return Response.redirect(REPO_URL, 302);
    }

    return new Response("not found\n", {
      status: 404,
      headers: { "content-type": "text/plain; charset=utf-8" },
    });
  },
};
