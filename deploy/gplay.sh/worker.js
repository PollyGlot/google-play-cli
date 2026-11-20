const REPO = "PollyGlot/google-play-cli";
const REPO_URL = `https://github.com/${REPO}`;
const LATEST_RELEASE_API = `https://api.github.com/repos/${REPO}/releases/latest`;
const LATEST_RELEASE_PAGE = `${REPO_URL}/releases/latest`;
const USER_AGENT = "gplay-site-worker";
const TAG_PATTERN = /^v\d+\.\d+\.\d+(?:-[0-9A-Za-z.]+)?$/;
const CACHE_SECONDS = 300;
const EDGE_CACHE = {
  cacheEverything: true,
  cacheTtlByStatus: { "200-399": CACHE_SECONDS, "400-599": 0 },
};

function rawInstallUrl(tag) {
  return `https://raw.githubusercontent.com/${REPO}/${tag}/install.sh`;
}

const APEX = "gplay.sh";
const DOCS_HOST = "docs.gplay.sh";
const WWW_HOST = "www.gplay.sh";

const LINK_HEADER = [
  '</docs/>; rel="service-doc"; type="text/html"',
  '</llms.txt>; rel="describedby"; type="text/plain"',
  '</.well-known/agent-skills/index.json>; rel="https://schemas.agentskills.io/discovery"; type="application/json"',
].join(", ");

const SECURITY_HEADERS = {
  "x-content-type-options": "nosniff",
  "x-frame-options": "DENY",
  "referrer-policy": "strict-origin-when-cross-origin",
  "permissions-policy": "camera=(), microphone=(), geolocation=()",
  "strict-transport-security": "max-age=31536000; includeSubDomains",
  "cross-origin-opener-policy": "same-origin",
};

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
      response = await env.ASSETS.fetch(request);
    }
    return withSecurityHeaders(response);
  },
};

async function handle(request, env) {
  const url = new URL(request.url);
  const { hostname, pathname } = url;

  if (hostname === DOCS_HOST || hostname === WWW_HOST) {
    const target = new URL(url);
    target.hostname = APEX;
    if (hostname === DOCS_HOST) {
      const rest = pathname === "/" ? "" : pathname;
      const isCanonicalDocsPath = rest === "/docs" || rest.startsWith("/docs/");
      target.pathname = isCanonicalDocsPath ? rest : `/docs${rest}`;
    }
    return Response.redirect(target.toString(), 301);
  }

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

    console.log({
      event: "install_hit",
      method: request.method,
      path: pathname,
      userAgent: request.headers.get("user-agent") || "",
      referer: request.headers.get("referer") || "",
      country: request.cf?.country || "",
      asOrganization: request.cf?.asOrganization || "",
    });

    const resolved = await resolveLatestTag();
    if (!resolved.tag) {
      console.warn({ event: "install_unresolved", errors: resolved.errors });
      return new Response(
        "could not resolve the latest gplay release; refusing to serve an unreleased installer.\n" +
          `Retry in a minute, or download a release from ${REPO_URL}/releases/latest\n`,
        {
          status: 503,
          headers: {
            "content-type": "text/plain; charset=utf-8",
            "cache-control": "no-store",
            "retry-after": "60",
          },
        },
      );
    }
    if (resolved.source !== "api") {
      console.warn({
        event: "install_ref_fallback",
        tag: resolved.tag,
        source: resolved.source,
        errors: resolved.errors,
      });
    }

    const upstream = await fetch(rawInstallUrl(resolved.tag), {
      method: request.method,
      cf: EDGE_CACHE,
    });
    if (!upstream.ok) {
      return new Response(
        `could not fetch the ${resolved.tag} installer (upstream ${upstream.status}). See ${REPO_URL}\n`,
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
        "x-gplay-installer-ref": resolved.tag,
      },
    });
  }

  if (request.method === "GET" && acceptsMarkdownFirst(request.headers.get("accept"))) {
    const markdown = await serveMarkdown(request, url, env);
    if (markdown) return markdown;
  }

  const response = await env.ASSETS.fetch(request);

  if (pathname.startsWith("/_astro/") && response.ok) {
    const immutable = new Response(response.body, response);
    immutable.headers.set("cache-control", "public, max-age=31536000, immutable");
    return immutable;
  }

  return decorateDocument(response);
}

async function resolveLatestTag() {
  const errors = [];
  try {
    const res = await fetch(LATEST_RELEASE_API, {
      headers: {
        accept: "application/vnd.github+json",
        "user-agent": USER_AGENT,
      },
      cf: EDGE_CACHE,
    });
    if (!res.ok) throw new Error(`api: status ${res.status}`);
    const tag = (await res.json())?.tag_name;
    if (typeof tag !== "string" || !TAG_PATTERN.test(tag)) {
      throw new Error(`api: unexpected tag_name ${JSON.stringify(tag)}`);
    }
    return { tag, source: "api", errors };
  } catch (err) {
    errors.push(String(err?.message ?? err));
  }
  try {
    const res = await fetch(LATEST_RELEASE_PAGE, {
      method: "HEAD",
      redirect: "manual",
      headers: { "user-agent": USER_AGENT },
      cf: EDGE_CACHE,
    });
    const location = res.headers.get("location") ?? "";
    const tag = location.match(/\/releases\/tag\/([^/?#]+)$/)?.[1] ?? "";
    if (!TAG_PATTERN.test(tag)) {
      throw new Error(`release page: status ${res.status}, location ${JSON.stringify(location)}`);
    }
    return { tag, source: "release-page", errors };
  } catch (err) {
    errors.push(String(err?.message ?? err));
  }
  return { tag: null, source: null, errors };
}

function acceptsMarkdownFirst(accept) {
  if (!accept) return false;
  let markdownQ = -1;
  let htmlQ = -1;
  for (const part of accept.split(",")) {
    const [rawType, ...params] = part.trim().split(";");
    const type = rawType.trim().toLowerCase();
    let q = 1;
    for (const p of params) {
      const m = p.trim().match(/^q=([0-9.]+)$/i);
      if (m) q = parseFloat(m[1]);
    }
    if (type === "text/markdown" || type === "text/x-markdown") {
      markdownQ = Math.max(markdownQ, q);
    } else if (type === "text/html") {
      htmlQ = Math.max(htmlQ, q);
    }
  }
  return markdownQ > 0 && markdownQ >= htmlQ;
}

function markdownCandidates(pathname) {
  if (pathname === "/" || pathname === "/index.html") return ["/llms.txt"];
  const trimmed = pathname.replace(/\.html$/, "").replace(/\/$/, "");
  return [`${trimmed}.md`, `${trimmed}/index.md`];
}

async function serveMarkdown(request, url, env) {
  const conditional = new Headers();
  for (const h of ["if-none-match", "if-modified-since"]) {
    const value = request.headers.get(h);
    if (value) conditional.set(h, value);
  }

  for (const candidate of markdownCandidates(url.pathname)) {
    const assetUrl = new URL(url);
    assetUrl.pathname = candidate;
    assetUrl.search = "";
    const asset = await env.ASSETS.fetch(
      new Request(assetUrl, { method: "GET", headers: conditional }),
    );
    if (asset.status !== 200 && asset.status !== 304) continue;

    const out = new Response(asset.status === 304 ? null : asset.body, asset);
    if (asset.status === 200) {
      out.headers.set("content-type", "text/markdown; charset=utf-8");
    }
    out.headers.set("cache-control", `public, max-age=${CACHE_SECONDS}`);
    const vary = out.headers.get("vary");
    out.headers.set("vary", vary ? `${vary}, Accept` : "Accept");
    const link = out.headers.get("link");
    out.headers.set("link", link ? `${link}, ${LINK_HEADER}` : LINK_HEADER);
    return out;
  }
  return null;
}

function decorateDocument(response) {
  const contentType = response.headers.get("content-type") || "";
  if (!contentType.includes("text/html")) return response;
  const decorated = new Response(response.body, response);
  const link = decorated.headers.get("link");
  decorated.headers.set("link", link ? `${link}, ${LINK_HEADER}` : LINK_HEADER);
  const vary = decorated.headers.get("vary");
  decorated.headers.set("vary", vary ? `${vary}, Accept` : "Accept");
  return decorated;
}
