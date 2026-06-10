# CI/CD with gplay

This guide covers the minimum to wire `gplay` into a CI pipeline. The
canonical example uses GitHub Actions. The same patterns apply to any other
provider (GitLab CI, Bitrise, CircleCI, Jenkins) — the only thing that
changes is how secrets are injected.

## 1. Create the service account

`gplay` authenticates to Google with a **Google Cloud service account** that
has been granted access to your Play Console app. One-time setup:

1. **Google Cloud Console** → Create or pick a project → IAM & Admin →
   Service accounts → Create service account.
2. **Keys tab** → Add key → JSON. Download the `*.json` file. Treat it as a
   secret — this key can publish to your store listings.
3. **Play Console** → Setup → API access → Link the GCP project that owns
   the service account → grant the service account the permissions your
   workflow needs:
   - "Release apps to production, exclude devices, and use Play App
     Signing" → required for `releases upload`, `promote`, rollout verbs.
   - "Reply to reviews" → required for `reviews reply`.
   - Whatever else maps to the commands you'll run.
4. Verify locally with `gplay auth doctor --package <your.package>` — it
   round-trips an `edits.insert` + `edits.delete` against your app and will
   tell you exactly what's missing.

> The single most common error is **service account created but not
> invited on the app in Play Console**. `gplay auth doctor` is built to
> catch this.

### Grant least privilege — one scoped account per job

The real authority boundary for CI and agent use is the **Play Console
permission set of the service account**, not the flags a workflow passes. Don't
mint one admin-everything account and reuse it everywhere: grant each workflow
*only* the permissions its commands need, and mint a **separate service account
per archetype** so a leaked key from (say) a metadata job can't publish a
release.

| Archetype | gplay commands it runs | Play Console permissions to grant |
|---|---|---|
| **Read-only reporting** (dashboards, **AI agents**) | `apps list/view`, `tracks list/view`, `releases list`, `reviews list`, `team … list`, `metadata pull`, `schema` | **"View app information and download bulk reports (read-only)"** — and, for vitals, "View app quality information (Android vitals)" |
| **Release-only** | `releases upload/promote/rollout/halt/resume/complete`, `tracks create`, `testers set` | "Release to production, exclude devices, and use Play App Signing"; "Release apps to testing tracks"; "Manage testing tracks and edit tester lists" |
| **Metadata-only** | `metadata apply`, `metadata images apply`, `apps details set` | "Manage store presence" |
| **Reviews** | `reviews reply` | "Reply to reviews" |
| **Team administration** | `team users add/set/remove`, `team grants set/remove` | Account-level **"Admin (all permissions)"** — managing users and their access is an account-level capability; grant it to the *narrowest* set of automations |

Two reinforcing controls, use both:

- **A read-only service account** for every dashboard and AI agent. With only
  "View app information (read-only)" granted, a mutating call fails at the API
  with **exit 11** (authorization) even if something tries one.
- **`GPLAY_READONLY=1`** in the agent/dashboard environment. This refuses every
  mutating command *before* any network call (**exit 4**), so the boundary
  holds in the harness regardless of the flags an agent chooses — defence in
  depth on top of the scoped credential. See [DESIGN §8](DESIGN.md#8-verbosity-and-logging)
  and [ADR-0024](adr/0024-readonly-environment-policy.md).

Verify any scoped account end-to-end with
`gplay auth doctor --package <your.package>` before wiring it into a job.

## 2. Inject the credential into CI

In CI, **never** use `gplay auth login`. Always pass the credential through
the environment.

`GPLAY_SERVICE_ACCOUNT` accepts either a file path or the **JSON content
inline**. In CI, inline is the right choice — no temp files to clean up, no
disk write of the private key.

### GitHub Actions

Store the JSON content (the entire file, as-is) as a repository secret named
`GPLAY_SERVICE_ACCOUNT`.

```yaml
# .github/workflows/release.yml
name: Release to Play Store

on:
  push:
    tags: ['v*']

jobs:
  release:
    runs-on: ubuntu-latest
    env:
      GPLAY_SERVICE_ACCOUNT: ${{ secrets.GPLAY_SERVICE_ACCOUNT }}
    steps:
      - uses: actions/checkout@v6

      # Build your AAB however you do today (Gradle, Bazel, ...).
      - uses: actions/setup-java@v5
        with:
          distribution: temurin
          java-version: '17'
      - run: ./gradlew bundleRelease

      # Install gplay (pick one).
      - run: curl -fsSL https://gplay.sh/install | sh
      # or: brew install <org>/gplay/gplay
      # or: go install github.com/<org>/google-play-cli/cmd/gplay@latest

      # Verify auth before any mutating call.
      - run: gplay auth doctor --package com.example.myapp

      # Upload to the internal track.
      - run: |
          gplay releases upload app/build/outputs/bundle/release/app-release.aab \
            --package com.example.myapp \
            --track internal \
            --release-notes-dir ./whatsnew
```

### Credential hygiene — env var, never a flag

Pass the credential through **`GPLAY_SERVICE_ACCOUNT` (env)**, never through the
`--service-account` flag, when the value is inline JSON:

- A flag value lands in **shell history** and is visible in the **process
  listing** (`ps`, `/proc/<pid>/cmdline`) to any other process on the runner —
  your private key, exposed.
- An env var is not in `ps` output and not in shell history.

`--service-account` is for a **path** in interactive/local use; in CI the
credential is inline JSON in the environment. (`gplay auth login` is also
out — it writes the key to the runner's keystore; see §2.)

> **GitHub Actions secret masking.** A value stored as a repository/organization
> **secret** and referenced as `${{ secrets.GPLAY_SERVICE_ACCOUNT }}` is
> registered for log masking — if it ever surfaces in a log line, Actions
> redacts it as `***`. Masking is best-effort, not a license to print it:
> multi-line JSON can defeat line-based masking, so still never `echo` the
> credential. Storing the JSON as a **variable** (`vars.*`) instead of a secret
> gets you no masking at all.

## 3. Typical release flow

A staged rollout to production usually looks like this, spread across one
or more workflows:

```bash
# 1. Push every CI build to internal — visible to your internal testers
gplay releases upload app.aab --package com.example.myapp --track internal

# 2. Promote a green build to beta — same versionCode, no re-upload
gplay releases promote --package com.example.myapp --from internal --to beta

# 3. Promote to production. Defaults to `draft` (see ADR-0002).
gplay releases promote --package com.example.myapp --from beta --to production

# 4. Start a staged rollout when ready.
gplay releases rollout --package com.example.myapp --track production --to 0.05

# 5. Ramp up over the next few days.
gplay releases rollout --package com.example.myapp --track production --to 0.20
gplay releases rollout --package com.example.myapp --track production --to 0.50
gplay releases complete --package com.example.myapp --track production

# Or halt if metrics go bad.
gplay releases halt --package com.example.myapp --track production
```

## 4. Exit codes — retry vs. fail

In CI scripts, decide whether to retry based on the exit code. The full
table is in [`DESIGN.md`](DESIGN.md#9-exit-codes); the short version:

- `0` → success
- `40`, `50` → upstream/network blip, **safe to retry**
- `10`, `11`, `20`, `30`, `60` → won't get better by retrying; surface the
  error
- `2` → CLI usage bug in your workflow
- `4` → denied by `GPLAY_READONLY`; **not** retryable, and not fixable by a flag

### Prefer `--retry` over a hand-rolled loop

The transient classes above (transport errors, 5xx, 429) are exactly what the
global **`--retry N`** flag handles for you — so you don't re-implement the loop
in every pipeline:

```bash
# Retry transient failures up to 3 times with exponential backoff + jitter;
# 429 honors Retry-After. Non-transient failures (auth, validation, conflict)
# fail fast. With --retry, --timeout bounds each attempt.
gplay releases upload app.aab --package com.example.myapp --track internal \
  --retry 3 --timeout 2m
```

`--retry` defaults to `0` (no retry — today's behavior). It **never** retries
`edits.commit` (a duplicate could double-publish) or non-transient 4xx, so it is
safe to leave on. A retried upload re-sends its bundle from a fresh reader.

If you still want shell-level control (e.g. to retry across *separate* commands,
or to add alerting), branch on the exit code yourself:

```bash
for attempt in 1 2 3; do
  gplay releases upload app.aab --package com.example.myapp --track internal
  code=$?
  case $code in
    0)        exit 0 ;;
    40|50)    echo "transient (exit $code), retrying..."; sleep $((attempt * 10)) ;;
    *)        exit $code ;;
  esac
done
exit 1
```

## 5. Verify a release before trusting it

The checksums and binaries on the release page share one origin, so a checksum
check alone proves integrity, not provenance — an origin compromise falsifies
both together. Each release therefore ships two origin-independent proofs you
can gate on before letting `gplay` into a pipeline:

- a **GitHub build-provenance attestation** over every archive, and
- a **keyless cosign signature** over `checksums.txt` (which transitively
  covers every archive it lists).

Pin a verification step into the job that installs `gplay`:

```yaml
      - name: Install and verify gplay
        env:
          GH_TOKEN: ${{ github.token }}
          VERSION: v0.5.0
        run: |
          set -euo pipefail
          base="https://github.com/PollyGlot/google-play-cli/releases/download/$VERSION"
          archive="gplay_${VERSION#v}_linux_amd64.tar.gz"
          curl -fsSLO "$base/$archive"

          # Provenance: built by this repo's release workflow.
          gh attestation verify "$archive" -R PollyGlot/google-play-cli

          # Signature over the checksum file, then the archive against it.
          curl -fsSLO "$base/checksums.txt"
          curl -fsSLO "$base/checksums.txt.sigstore.json"
          cosign verify-blob checksums.txt \
            --bundle checksums.txt.sigstore.json \
            --certificate-identity-regexp '^https://github.com/PollyGlot/google-play-cli/\.github/workflows/release\.yml@' \
            --certificate-oidc-issuer https://token.actions.githubusercontent.com
          sha256sum -c <(grep " $archive$" checksums.txt)

          tar -xzf "$archive" gplay && sudo install -m0755 gplay /usr/local/bin/gplay
```

`gh attestation verify` needs only the GitHub CLI (preinstalled on GitHub
runners) and `GH_TOKEN`; `cosign verify-blob` needs `cosign` on `PATH`
(`sigstore/cosign-installer`). Either one alone is a meaningful gate; running
both is belt-and-suspenders. The `install.sh` one-liner already verifies the
SHA-256 against `checksums.txt` and fails closed (see the README), so for many
pipelines the attestation check above is the only thing you need to add.

## 6. Migration from `fastlane supply`

Coming from Fastlane, the single most common surprise is that
`gplay releases upload --track production` does **not** publish 100% by
default — it creates a `draft` release that you (or a follow-up command)
must explicitly promote with `--complete` or `--staged <fraction>`. This is
deliberate; see [ADR-0002](adr/0002-safe-production-defaults.md). On every
other track the behavior matches Fastlane (`completed` at 100%).

Fully detailed migration table: backlog item, to be added once the MVP is
stable and real migrators give feedback on the pitfalls.
