# gplay.sh — install-script edge proxy

A single Cloudflare Worker that serves the gplay install script behind a short,
memorable URL:

```bash
curl -fsSL https://gplay.sh/install | sh
```

The Worker ([`worker.js`](worker.js)) proxies [`install.sh`](../../install.sh)
from the repo's `main` branch and returns it as `text/plain`. The repo stays the
single source of truth — there is no static copy to drift. Rationale:
[ADR-0009](../../docs/adr/0009-install-distribution-vanity-domain.md).

## Routes

| Path | Behaviour |
| --- | --- |
| `GET /install` (or `/install.sh`) | proxies `install.sh` from `main`, `text/plain`, 5 min cache |
| `GET /` | 302 → the GitHub repo |
| anything else | 404 |

## Deploy

**The Worker deploys automatically.** Any push to `main` that touches
`deploy/gplay.sh/**` triggers
[`.github/workflows/deploy-worker.yml`](../../.github/workflows/deploy-worker.yml),
which runs `wrangler deploy` for you. The trigger is the *path*, not the kind of
merge: a CLI release or an `install.sh` edit does **not** redeploy the Worker —
the Worker proxies `install.sh` live from `main`. See
[ADR-0009](../../docs/adr/0009-install-distribution-vanity-domain.md).

This needs one repo secret, `CLOUDFLARE_API_TOKEN`:

1. Cloudflare dashboard → **My Profile → API Tokens → Create Token**, custom
   token with **only** `Account → Workers Scripts → Edit`, scoped to the one
   account that owns this Worker. Suggested token name:
   `gplay-install-worker-deploy (GitHub Actions)`.
2. Repo → **Settings → Secrets and variables → Actions → New repository
   secret**, name `CLOUDFLARE_API_TOKEN`.

The token stays minimal on purpose (public repo): it can republish the Worker
script and nothing else. The one-time custom-domain binding below needs broader
zone permissions and is therefore done **manually**, never from CI.

### Manual deploy (first-time domain binding / dispatch / debugging)

Wrangler needs to authenticate to Cloudflare once. Either:

```bash
# interactive browser login (recommended, nothing to store)
npx wrangler login
```

or set a scoped API token in the environment:

```bash
export CLOUDFLARE_API_TOKEN=…
```

Then, from this directory:

```bash
cd deploy/gplay.sh
npx wrangler deploy
```

This publishes to `https://gplay-install.<your-subdomain>.workers.dev`. Smoke
test the full chain before touching any domain:

```bash
curl -fsSL https://gplay-install.<your-subdomain>.workers.dev/install | sh
```

## Attaching the gplay.sh domain (later)

1. Register `gplay.sh` (available as of writing; ~$31 first year / ~$47 renew).
2. Add the zone to this Cloudflare account and point the registrar's
   nameservers at Cloudflare.
3. Uncomment the `routes` block in [`wrangler.toml`](wrangler.toml) and
   `npx wrangler deploy` again.
4. Verify end-to-end: `curl -fsSL https://gplay.sh/install | sh`.
5. Only then flip the references (separate PR), per ADR-0009:
   - `README.md` → `https://gplay.sh/install`
   - `.goreleaser.yaml` `release.header` → same

Keep the raw GitHub URL working until `gplay.sh` is verified live.
