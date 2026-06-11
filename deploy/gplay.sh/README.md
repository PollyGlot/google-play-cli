# gplay.sh — site + install-script Cloudflare Worker

A single Cloudflare Worker (`gplay-site`) that serves the gplay **website** and
the **install script** from one deployable:

```bash
curl -fsSL https://gplay.sh/install | sh
```

The website ([`website/`](../../website/)) is built to `website/dist` and
uploaded as Worker **static assets**; the `/install` endpoint is dynamic —
[`worker.js`](worker.js) proxies [`install.sh`](../../install.sh) from the repo's
`main` branch as `text/plain`, so the repo stays the single source of truth with
no static copy to drift. Rationale:
[ADR-0009](../../docs/adr/0009-install-distribution-vanity-domain.md) (install
endpoint) and
[ADR-0025](../../docs/adr/0025-website-served-from-install-worker.md) (serving
the site from the same Worker).

## Routes

| Request | Behaviour |
| --- | --- |
| `GET /install` (or `/install.sh`) | proxies `install.sh` from `main`, `text/plain`, 5 min cache |
| `docs.gplay.sh/<path>` | 301 → `https://gplay.sh/docs/<path>` |
| `www.gplay.sh/<path>` | 301 → `https://gplay.sh/<path>` |
| anything else | the static Astro site (landing + docs), with its own 404 page |

`run_worker_first = true` in [`wrangler.toml`](wrangler.toml) is what lets the
handler intercept `/install` and the docs/www hostnames before falling through
to asset serving.

## Deploy

**The Worker deploys automatically.** Any push to `main` that touches
`website/**` or `deploy/gplay.sh/**`, every published release (to refresh the
generated CLI reference), and *workflow dispatch* trigger
[`.github/workflows/deploy-site.yml`](../../.github/workflows/deploy-site.yml),
which builds the gplay binary, builds the site, and runs `wrangler deploy` for
you.

This needs one repo secret, `CLOUDFLARE_API_TOKEN`, and one repo variable,
`CLOUDFLARE_ACCOUNT_ID`:

1. Cloudflare dashboard → **My Profile → API Tokens → Create Token**, custom
   token with **only** `Account → Workers Scripts → Edit`, scoped to the one
   account that owns this Worker. Suggested name: `gplay-site-deploy (GitHub
   Actions)`. (Workers Scripts:Edit covers the static-assets upload.)
2. Repo → **Settings → Secrets and variables → Actions → New repository
   secret**, name `CLOUDFLARE_API_TOKEN`. Add the account ID as a repository
   **variable** named `CLOUDFLARE_ACCOUNT_ID` (it is config, not a credential).

The token stays minimal on purpose (public repo): it can republish the Worker
and its assets and nothing else. The one-time custom-domain binding below needs
broader zone permissions and is therefore done **manually**, never from CI.

### Manual deploy (first-time domain binding / dispatch / debugging)

The assets must exist before `wrangler deploy`, so build the site first:

```bash
# from the repo root
make build                       # produces bin/gplay for the CLI reference
cd website && npm ci && npm run build && cd -
```

Authenticate to Cloudflare once — either an interactive login (nothing to
store) or a scoped token in the environment:

```bash
npx wrangler login
# or
export CLOUDFLARE_API_TOKEN=…
export CLOUDFLARE_ACCOUNT_ID=…   # needed if the token can't auto-discover it
```

Then deploy from this directory:

```bash
cd deploy/gplay.sh
npx wrangler deploy
```

Without a custom domain this publishes to
`https://gplay-site.<your-subdomain>.workers.dev`. Smoke-test the install chain
and the site before touching DNS:

```bash
curl -fsSL https://gplay-site.<your-subdomain>.workers.dev/install | sh
open  https://gplay-site.<your-subdomain>.workers.dev/
```

## Attaching the gplay.sh domains (one-time, manual)

Done in the dashboard because it needs zone DNS/Routes edit — permissions the CI
token deliberately lacks (ADR-0009). Prerequisite: the `gplay.sh` zone is added
to this Cloudflare account and the registrar's nameservers point at Cloudflare
(Porkbun → Cloudflare, already done).

1. **Workers & Pages → `gplay-site` → Settings → Domains & Routes → Add → Custom
   domain.** Add each of:
   - `gplay.sh` (apex)
   - `www.gplay.sh` (optional; the Worker 301s it to the apex)
   - `docs.gplay.sh` (the Worker 301s it to `gplay.sh/docs`)

   Cloudflare creates the proxied DNS records and issues TLS certificates
   automatically — no manual `A`/`CNAME` entries needed for custom domains.

2. Verify end-to-end:

   ```bash
   curl -fsSL https://gplay.sh/install | sh           # installer
   curl -sI   https://docs.gplay.sh/ | grep -i location  # → https://gplay.sh/docs/
   curl -sI   https://www.gplay.sh/  | grep -i location  # → https://gplay.sh/
   open       https://gplay.sh/                        # landing
   open       https://gplay.sh/docs/                   # docs
   ```

3. Only **after** `https://gplay.sh/install` is verified live, flip the
   remaining published references in a separate PR, per ADR-0009 — the repo
   `README.md`, `.goreleaser.yaml` `release.header`, and the `install.sh` header
   comment still point at the raw GitHub URL as the working fallback.

The raw GitHub URL keeps working throughout, so nothing breaks if a binding step
is delayed.
