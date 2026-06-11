# Serve the install script from a vanity domain via a Cloudflare Worker proxy

The `curl … | sh` entry point is served at `https://gplay.sh/install` by a
Cloudflare Worker that proxies [`install.sh`](../../install.sh) from the repo's
`main` branch as `text/plain`. The Worker, its config, and deploy notes live in
[`deploy/gplay.sh/`](../../deploy/gplay.sh/).

Status: accepted (the install-proxy decision below stands as written);
**extended — not superseded — by [ADR-0025](./0025-website-served-from-install-worker.md)**,
which puts the website on this same Worker (see the update note below).

**Update — [ADR-0025](./0025-website-served-from-install-worker.md):** the
`gplay.sh` domain is now registered and on Cloudflare, and the full website
(issue #86, listed as out of scope below) is served from **this same Worker**
via static assets. The deploy automation described under "When and how the
Worker is deployed" was consolidated into `deploy-site.yml` accordingly. The
repository root `README.md`, `.goreleaser.yaml`, and `install.sh` header now
point at `https://gplay.sh/install` (flipped once the domain went live — issue
#85). The Worker still proxies `install.sh` from the raw GitHub URL on `main`,
which also stays a working fallback.

## Why

1. **A short, trustworthy install URL.** `curl -fsSL https://gplay.sh/install | sh`
   reads as a real product; the raw `raw.githubusercontent.com/PollyGlot/…/main/install.sh`
   does not. It also mirrors the sibling project `asc` (`asccli.sh`), aligning
   the two CLIs down to the install command.

2. **Worker proxy, not a static copy.** The Worker fetches `install.sh` from
   `main` on each request (with a short cache). The repo stays the single source
   of truth — there is no second copy to forget to redeploy. This is the main
   reason to prefer a Worker over hosting the script as a static asset.

3. **`main`, not a tag.** `install.sh` resolves the latest release itself via the
   GitHub API, so proxying `main` means the endpoint always serves the newest
   installer without any per-release deploy step.

4. **HTTPS is non-negotiable for `curl | sh`.** Serving from a domain we control,
   over TLS, on Cloudflare's edge, is the baseline security posture for a piped
   shell installer.

## What we lose

- **A recurring cost and a renewal to remember.** `.sh` is a premium TLD
  (~$31 first year, ~$47/year after). Letting it lapse would break every
  published install command, so it becomes a standing operational commitment for
  as long as the project ships.
- **An extra hop in the install path.** Cloudflare sits between the user and
  GitHub. If the Worker is misconfigured or Cloudflare has an incident, installs
  fail even when GitHub is healthy. Mitigated by keeping the raw GitHub URL as a
  documented fallback.

## Considered options

- **Raw GitHub URL (status quo).** Free, zero maintenance, already live — but
  long, ugly, and visibly GitHub-bound. Kept as the fallback and as the value
  the published references use until `gplay.sh` is live.
- **302 redirect to raw GitHub** (a Cloudflare redirect rule, no Worker).
  Simplest, and `curl -fsSL` follows redirects (`-L`). Rejected as the primary
  mechanism: it leaks the GitHub URL, can't pin a version or add headers, and
  depends on redirect-following. The Worker is barely more code and strictly
  more controllable.
- **Static copy of the script** deployed alongside a Worker/Pages site (what
  `asc` appears to do). Rejected: reintroduces drift between the served script
  and `install.sh` in the repo.
- **A full landing site at `gplay.sh`** (docs, wall-of-apps, like `asccli.sh`).
  Out of scope *for this ADR* — a much larger, orthogonal effort tracked as
  issue #86. Now decided in
  [ADR-0025](./0025-website-served-from-install-worker.md): the site rides on
  this same Worker as static assets.

## When and how the Worker is deployed

The Worker is **Deployed** (`wrangler deploy`) only when its own code changes
(`deploy/gplay.sh/**`) — never on a CLI **Release** and never when `install.sh`
changes. This decoupling is the whole point of the proxy: `install.sh` is fetched
live from `main`, and the installer resolves the latest release itself, so neither
a script edit nor a new CLI version requires touching the Worker. The only changes
that warrant a Deploy are edits to `worker.js` / `wrangler.toml` (e.g. attaching the
`gplay.sh` custom domain, adjusting cache or routes) — expected to happen a handful
of times in the project's life.

Deployment was originally automated via `deploy-worker.yml`, gated on
`push: main` **path-filtered to `deploy/gplay.sh/**`**. The trigger is the *path*,
not the *kind of merge*: the workflow stayed dormant on the vast majority of PRs and
fired only on the rare Worker-code change. (Since [ADR-0025](./0025-website-served-from-install-worker.md)
this is `deploy-site.yml`, which additionally fires on `website/**` and on
published releases because the Worker now carries the site assets too.)

### Why automate something that runs twice a year

Not to save time — there is none to save on two events. The value is eliminating
**silent drift**: a Worker-code change (typically uncommenting the `routes` block to
bind `gplay.sh`) that is merged but never `wrangler deploy`-ed leaves git and
production disagreeing with no error surfaced. That is precisely the failure mode
this ADR's "single source of truth" stance exists to prevent for the script — so we
prevent it for the Worker code too.

### Token scope on a public repo

The deploy secret (`CLOUDFLARE_API_TOKEN`) is deliberately **minimal**: account-level
`Workers Scripts:Edit`, nothing more. On a public OSS repo a leaked secret is a
high-value target, so the token can at worst republish the Worker script — it cannot
edit DNS or create routes. The one-time binding of the `gplay.sh` custom domain
(which *does* need `DNS:Edit` + `Workers Routes:Edit` on the zone) is done **manually**
with a local `wrangler deploy`, so those broader permissions never live in repo
secrets. DNS stays out of CI permanently; only Worker-code publishing is automated.

Keeping the token that minimal has one consequence: `wrangler deploy` cannot
auto-discover the account (that lookup hits `/memberships`, which needs
`Memberships:Read` — i.e. User → Memberships → Read), so it would fail with
`Authentication error [code: 10000]`. (Wrangler's own error text points at
`User Details:Read`, but that scope only governs the email shown by `whoami`;
the account lookup itself is gated by `Memberships:Read`.)
The fix is to pass the account ID **explicitly** rather than broaden the token —
via the `accountId` input of `cloudflare/wrangler-action`, sourced from the GitHub
Actions variable `vars.CLOUDFLARE_ACCOUNT_ID`.

The account ID is **config, not a credential** — it grants nothing on its own and
appears in dashboard URLs — so committing it to `wrangler.toml` would be harmless.
We still keep it out of this public file, as a GitHub Actions *variable* (not a
secret: a variable is the honest classification for non-sensitive config). This
mirrors the reference project `RhysSullivan/executor`, which likewise never commits
its account ID and sources it from CI configuration. The net effect: nothing in
the repo identifies the Cloudflare account, and the deploy token remains
`Workers Scripts:Edit` only.

The `push: main` trigger is also what keeps the secret safe from fork PRs: it runs
only on already-merged (maintainer-reviewed) code, never on untrusted `pull_request`
events, so the token is never exposed to a contributor's fork.

## Note on tooling

The Cloudflare MCP server connected to this project is **read-only for Workers**
(`workers_list`, `workers_get_worker`, `workers_get_worker_code`) and exposes no
DNS tools. Deployment is therefore done with Wrangler locally
(`wrangler login` + `wrangler deploy`), not via MCP. See
[`deploy/gplay.sh/README.md`](../../deploy/gplay.sh/README.md).
