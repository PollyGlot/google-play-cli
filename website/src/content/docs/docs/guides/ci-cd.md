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
`GPLAY_SERVICE_ACCOUNT`.

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

      # Install gplay.
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

## Retry on transient failures

Exit codes `40` (API 5xx) and `50` (network) are safe to retry; everything
else is terminal:

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

## Related

- [Release flow](/docs/guides/release-flow/)
- [Authentication & accounts](/docs/concepts/authentication/)
