# Serve the install script from a vanity domain via a Cloudflare Worker proxy

The `curl … | sh` entry point is served at `https://gplay.sh/install` by a
Cloudflare Worker that proxies [`install.sh`](../../install.sh) from the repo's
`main` branch as `text/plain`. The Worker, its config, and deploy notes live in
[`deploy/gplay.sh/`](../../deploy/gplay.sh/).

Status: accepted in principle. The `gplay.sh` domain is not yet registered, so
the README and `.goreleaser.yaml` still point at the raw GitHub URL; they flip
to `gplay.sh` in a separate PR only once the domain is verified live. Remaining
work is tracked in issue #85; a full doc site (rejected below) in issue #86.

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
  Out of scope here — a much larger, orthogonal effort tracked separately. This
  ADR covers only the install endpoint.

## Note on tooling

The Cloudflare MCP server connected to this project is **read-only for Workers**
(`workers_list`, `workers_get_worker`, `workers_get_worker_code`) and exposes no
DNS tools. Deployment is therefore done with Wrangler locally
(`wrangler login` + `wrangler deploy`), not via MCP. See
[`deploy/gplay.sh/README.md`](../../deploy/gplay.sh/README.md).
