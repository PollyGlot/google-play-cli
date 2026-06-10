---
title: CI/CD integration
description: Wire gplay into GitHub Actions or any CI — inject the service account as a secret, verify with auth doctor, upload releases, and retry on semantic exit codes.
sidebar:
  order: 2
---

gplay is built for CI: one static binary, no runtime, JSON output by default
when piped, and [exit codes](/docs/concepts/exit-codes/) that make retry
decisions trivial. The example below is GitHub Actions; the same pattern
applies to GitLab CI, Bitrise, CircleCI, or Jenkins — only the secret
injection changes.

## Inject the credential

In CI, **never** use `gplay auth login`. Pass the credential through the
environment: `GPLAY_SERVICE_ACCOUNT` accepts a file path or the **JSON
content inline**. Inline is the right choice in CI — no temp file, no
private key written to disk.

Store the entire service-account JSON as a repository secret named
`GPLAY_SERVICE_ACCOUNT`, then pass it through the **environment**, never the
`--service-account` flag: an inline-JSON flag value lands in shell history and
in the process listing (`ps`, `/proc/<pid>/cmdline`), exposing your private
key. An env var is in neither. (`--service-account` is for a *path* in local
use.)

:::tip[Least privilege]
The real authority boundary is the service account's **Play Console
permission set**, not the flags a job passes. Mint a separate, narrowly-scoped
account per job archetype — a leaked metadata-job key then can't publish a
release. Give dashboards and AI agents a **read-only** account, and add
`GPLAY_READONLY=1` to their environment as defence in depth: every mutating
command is refused with exit `4` before any network call, whatever flags the
agent chooses. See [gplay for AI agents](/docs/agents/agent-guide/).
:::

## GitHub Actions workflow

```yaml
# .github/workflows/play-release.yml
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

      # Install gplay (the script verifies the archive checksum and fails
      # closed). To also gate on provenance/signature, see "Verify the install"
      # below.
      - run: curl -fsSL https://raw.githubusercontent.com/PollyGlot/google-play-cli/main/install.sh | sh

      # Verify auth before any mutating call.
      - run: gplay auth doctor --package com.example.myapp

      # Upload to the internal track.
      - run: |
          gplay releases upload app/build/outputs/bundle/release/app-release.aab \
            --package com.example.myapp \
            --track internal \
            --release-notes-dir ./whatsnew
```

JSON output needs no flag in CI: gplay emits JSON when stdout is not a TTY
(piped/captured output) **or** when `CI=true` is set — each condition
triggers it on its own, and CI runners satisfy both. An explicit
`--output table` remains the override if you ever want human-shaped logs.
See [output formats](/docs/concepts/output-formats/).

## Bound and retry transient failures

Prefer the built-in `--retry` over a hand-rolled loop. It retries the
transient classes (transport errors, 5xx, 429 honouring `Retry-After`) with
exponential backoff, and never retries non-transient 4xx or `edits.commit`:

```bash
gplay releases upload app.aab --package com.example.myapp --track internal \
  --retry 3 --timeout 2m
```

`--timeout` bounds each request (60s default for control-plane calls; uploads
exempt unless set), so a hung connection fails in seconds instead of stalling
the job. With `--retry`, it's a per-attempt bound.

When you need shell-level control — retrying across *separate* commands, or
adding alerting — branch on the [exit code](/docs/concepts/exit-codes/)
yourself (`40`/`50` are retry-safe; `4`, from `GPLAY_READONLY`, is not, and
isn't fixable by a flag):

```bash
for attempt in 1 2 3; do
  gplay releases upload app.aab --package com.example.myapp --track internal
  code=$?
  case $code in
    0)     exit 0 ;;
    40|50) echo "transient (exit $code), retrying..."; sleep $((attempt * 10)) ;;
    *)     exit $code ;;
  esac
done
exit 1
```

## Splitting the pipeline

A common shape across workflows:

1. **Every merge to main** → `releases upload --track internal`
2. **Manual or scheduled promotion** → `releases promote --from internal --to beta`
3. **Release tag** → `releases promote --from beta --to production` (lands
   as a draft), then a human or a final job runs
   `releases rollout --track production --to 0.05 --confirm`

Google rate-limits publishing; as a rule of thumb, don't publish to
alpha/beta more than once a day, and less often to production.

## Verify the install in CI

To gate the pipeline on artifact provenance, install from the release
archive and verify it before running anything:

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
          shasum -a 256 -c <(grep " $archive$" checksums.txt)

          tar -xzf "$archive" && sudo install gplay /usr/local/bin/
```

See [Verify a release](/docs/getting-started/installation/) for the
standalone commands.

## Related

- [Release flow](/docs/guides/release-flow/)
- [Authentication & accounts](/docs/concepts/authentication/)
- [gplay for AI agents](/docs/agents/agent-guide/)
