// Cloudflare Worker for gplay.sh — serves the gplay website and the install
// script from one deployable.
//
//   GET /install (or /install.sh)  -> proxies install.sh from the repo's `main`
//                                      branch as text/plain (the `curl … | sh`
//                                      entry point)
//   docs.gplay.sh/<path>           -> 301 to https://gplay.sh/docs/<path>
//   www.gplay.sh/<path>            -> 301 to https://gplay.sh/<path>
//   everything else                -> the static Astro site (env.ASSETS)
//
// The install script is proxied from `main` (not a tag) on purpose: install.sh
// resolves the latest release itself, so `main` always serves the newest
// installer with the repo as the single source of truth — no static copy to
// drift. The website is built into website/dist and uploaded as Worker static
// assets on each deploy. `run_worker_first` is set so this handler runs ahead
// of asset serving, which is what lets it intercept the docs/www hostnames and
// the /install path. See docs/adr/0009-install-distribution-vanity-domain.md
// and docs/adr/0025-website-served-from-install-worker.md.

const RAW_INSTALL_URL =
  "https://raw.githubusercontent.com/PollyGlot/google-play-cli/main/install.sh";
const REPO_URL = "https://github.com/PollyGlot/google-play-cli";
const CACHE_SECONDS = 300;

const APEX = "gplay.sh";
const DOCS_HOST = "docs.gplay.sh";
const WWW_HOST = "www.gplay.sh";

// Applied to every response (static assets, /install, redirects). No
// Content-Security-Policy here: Astro/Starlight inject inline scripts, so a
// strict script-src would break the site — CSP needs its own tested change.
const SECURITY_HEADERS = {
  "x-content-type-options": "nosniff",
  "x-frame-options": "DENY",
  "referrer-policy": "strict-origin-when-cross-origin",
  "permissions-policy": "camera=(), microphone=(), geolocation=()",
  "strict-transport-security": "max-age=31536000; includeSubDomains",
};

// Responses from env.ASSETS.fetch and Response.redirect have immutable
// headers, so re-wrap into a fresh Response before setting anything.
function withSecurityHeaders(response) {
  const hardened = new Response(response.body, response);
  for (const [name, value] of Object.entries(SECURITY_HEADERS)) {
    hardened.headers.set(name, value);
  }
  return hardened;
}

export default {
  async fetch(request, env) {
    let response;
    try {
      response = await handle(request, env);
    } catch {
      // If the routing logic above ever throws, degrade to plain asset
      // serving rather than a Cloudflare 1101 error page for the whole site.
      response = await env.ASSETS.fetch(request);
    }
    return withSecurityHeaders(response);
  },
};

async function handle(request, env) {
  const url = new URL(request.url);
  const { hostname, pathname } = url;

  // Canonicalise hostnames onto the apex. docs.gplay.sh is a memorable entry
  // point — not a second site — so it lands on the canonical docs; www is
  // folded into the apex. One set of URLs to index, no duplicate content.
  if (hostname === DOCS_HOST || hostname === WWW_HOST) {
    const target = new URL(url);
    target.hostname = APEX;
    if (hostname === DOCS_HOST) {
      const rest = pathname === "/" ? "" : pathname;
      // Only an exact /docs or a /docs/… path is already canonical, so
      // lookalikes like /docs2 still get the /docs prefix.
      const isCanonicalDocsPath = rest === "/docs" || rest.startsWith("/docs/");
      target.pathname = isCanonicalDocsPath ? rest : `/docs${rest}`;
    }
    return Response.redirect(target.toString(), 301);
  }

  // The install endpoint is dynamic (proxied live from `main`); everything
  // else is served from the static-asset bundle below.
  if (pathname === "/install" || pathname === "/install.sh") {
    if (request.method !== "GET" && request.method !== "HEAD") {
      return new Response("method not allowed\n", {
        status: 405,
        headers: {
          allow: "GET, HEAD",
          "content-type": "text/plain; charset=utf-8",
        },
      });
    }

    // Structured install-attribution log. Workers Logs indexes object fields,
    // so the Observability query builder can group hits by user agent (curl vs
    // CI vs browser) and by network owner (e.g. GitHub-hosted runners show up
    // under Microsoft/Azure). Deliberately no IP: AS org + country is enough
    // to attribute traffic without storing anything personal.
    console.log({
      event: "install_hit",
      method: request.method,
      path: pathname,
      userAgent: request.headers.get("user-agent") || "",
      referer: request.headers.get("referer") || "",
      country: request.cf?.country || "",
      asOrganization: request.cf?.asOrganization || "",
    });

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
      },
    });
  }

  // Static site (landing + docs). The assets binding handles HTML routing,
  // trailing slashes, and the 404 page (see wrangler.toml [assets]).
  return env.ASSETS.fetch(request);
}
