# Serve the website from the install Worker via Cloudflare static assets

## Status

accepted

## Context

Two pieces of gplay's public surface had grown up separately:

- The **install endpoint** (`gplay.sh/install`) — a Cloudflare Worker that
  proxies `install.sh` from `main`
  ([ADR-0009](./0009-install-distribution-vanity-domain.md)). ADR-0009 scoped
  itself deliberately narrowly and listed "a full landing site at `gplay.sh`"
  as **out of scope**, tracked as issue #86.
- The **website** (landing + docs) — an Astro/Starlight site under
  [`website/`](../../website/) whose CLI reference is generated from the gplay
  binary at build time, that ships `llms.txt`/`llms-full.txt`, and that was
  hosted on **GitHub Pages** as interim beta hosting (issue #86), under the
  project base path `/google-play-cli`.

The `gplay.sh` domain is now registered (Porkbun) and on Cloudflare, so #86's
"when the domain is live" condition is met. We need the site at `gplay.sh`, a
memorable `docs.gplay.sh` entry point, and the install endpoint to keep working
— without two hosting systems contending for the same hostname.

## Decision

**Serve the whole site from the install Worker as static assets.** One Worker
(renamed `gplay-install` → `gplay-site`) carries `website/dist` via an `[assets]`
binding and keeps `/install` as a dynamic route. `run_worker_first = true` lets
the handler intercept the dynamic/redirect cases before asset serving:

- `gplay.sh/install` (+ `/install.sh`) → proxied installer (unchanged).
- `docs.gplay.sh/<path>` → **301** to `gplay.sh/docs/<path>`.
- `www.gplay.sh/<path>` → **301** to `gplay.sh/<path>`.
- everything else → the static landing + docs, base `/`.

**GitHub Pages is retired.** The former `website.yml` and `deploy-worker.yml`
collapse into one `deploy-site.yml` that builds the binary, builds the site, and
`wrangler deploy`s the Worker with its assets. Triggers: pushes touching
`website/**` or `deploy/gplay.sh/**`, published releases (to refresh the
generated reference), and dispatch.

The minimal CI token (`Workers Scripts:Edit`) and the manual, out-of-band
custom-domain binding from ADR-0009 are **preserved** — `Workers Scripts:Edit`
also covers the static-assets upload, and DNS stays out of CI permanently.

## Why this shape

1. **One system owns `gplay.sh`.** A Cloudflare Pages project on the apex plus a
   Worker route for `/install` is two systems on one hostname with a route
   precedence to reason about. A single Worker with assets has no such seam.
2. **Reuses the existing trust boundary.** No new token scope, no second deploy
   credential — the same minimal token and the same "bind the domain by hand"
   rule from ADR-0009 carry over verbatim.
3. **The redirect lives in the repo.** `docs.gplay.sh`/`www` redirects are code
   in `worker.js`, versioned and reviewed, rather than dashboard redirect rules
   that drift out of sight — consistent with ADR-0009's single-source-of-truth
   stance.
4. **Keeps what the site is good at.** Staying on Astro/Starlight keeps the
   binary-generated CLI reference (docs can't drift from the tool) and the
   `llms.txt` agent surface, both of which a hosted docs SaaS would forfeit.

## What we lose / accept

- **The Worker runs on every request.** `run_worker_first = true` invokes the
  script even for cached static assets (a hostname check, then `env.ASSETS`).
  At this traffic it is negligible, and it is the price of owning the
  docs/www redirects in code rather than in dashboard config.
- **`docs.gplay.sh` is a redirect, not a second site.** Its URLs resolve to
  `gplay.sh/docs/...`. A true split (docs hosted at the subdomain root) would
  mean separating landing and docs into two builds and rewriting every internal
  `/docs/...` link — real ongoing maintenance for a cosmetic URL gain. Deferred;
  the redirect is forward-compatible with doing it later.

## Considered and rejected

- **Cloudflare Pages for the site + keep the install Worker.** Purpose-built
  static hosting with per-PR previews, but two systems on `gplay.sh` and a new
  `Cloudflare Pages:Edit` token scope. Rejected for the seam and the extra
  credential.
- **A hosted docs SaaS (e.g. Mintlify).** Forfeits
  the binary-generated reference and the tailored `llms.txt`, adds an external
  dependency and a paid tier for custom domains — at odds with gplay's
  "single static binary, zero runtime deps" positioning. The `docs.` subdomain
  *URL shape* is worth keeping; the vendor dependency is not.
- **Keep GitHub Pages, point a CNAME at it.** Leaves `/install` on a separate
  Worker and the site on a separate host, and Pages can't run the dynamic
  install proxy. No reason to keep two hosts once one Worker can do both.

## Supersedes

The "out of scope: a full landing site at `gplay.sh`" note in
[ADR-0009](./0009-install-distribution-vanity-domain.md) — that boundary was for
the install-endpoint PR; this ADR is where the site joins the same Worker.
ADR-0009's install-proxy rationale and token/DNS discipline remain in force.
