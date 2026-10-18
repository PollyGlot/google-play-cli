---
title: CI/CD integration
description: "Wire gplay into GitHub Actions or any CI: inject the service account as a secret, verify with auth doctor, upload releases, and retry on semantic exit codes."
sidebar:
  order: 2
---

gplay is built for CI: one static binary, no runtime, JSON output by default
when piped, and [exit codes](/docs/concepts/exit-codes/) that make retry
decisions trivial. The main example is GitHub Actions, followed by minimal
examples for [GitLab CI, Bitrise, CircleCI and Jenkins](#other-ci-providers):
only the secret injection and the install step change.

## Inject the credential

In CI, **never** use `gplay auth login`. Pass the credential through the
environment: `GPLAY_SERVICE_ACCOUNT` accepts a file path or the **JSON
content inline**. Inline is the right choice in CI: no temp file, no
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
account per job archetype, so a leaked metadata-job key can't publish a
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
      - run: curl -fsSL https://gplay.sh/install | sh

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
(piped/captured output) **or** when `CI=true` is set. Each condition
triggers it on its own, and CI runners satisfy both. An explicit
`--output table` remains the override if you ever want human-shaped logs.
See [output formats](/docs/concepts/output-formats/).

## Other CI providers

Each example below does what the GitHub Actions job does: expose the key as
`GPLAY_SERVICE_ACCOUNT`, check it with `gplay auth doctor`, then upload the AAB
to the `internal` track. Two things change from one provider to the next:
where the secret lives, and how `gplay` lands on the runner.

- **The [container image](/docs/getting-started/installation/#container-image)**
  (`ghcr.io/pollyglot/gplay`) fits wherever a step can call `docker run` (the
  CircleCI example). It cannot be the *job* image on a provider that runs your
  script inside that container (GitLab's Docker executor, CircleCI's `docker`
  executor, Jenkins `agent { docker { ... } }`): those need a shell, and the
  distroless image has none.
- **The install script** covers everything else. It verifies the archive
  checksum and fails closed; `GPLAY_VERSION` pins the release, and
  `GPLAY_INSTALL_DIR` picks the directory (default `$HOME/.local/bin`, often
  absent from a runner's `PATH`).

If a secret store mangles multi-line values, store the key on one line:
`jq -c . key.json` prints the same JSON minified, and the private key keeps its
`\n` escapes. Do not base64-encode it: gplay reads inline JSON or a file path,
nothing else.

### GitLab CI

Add `GPLAY_SERVICE_ACCOUNT` under *Settings → CI/CD → Variables* with the JSON
as its value, and tick **Protected** so only protected branches and tags
receive it (protect your release tags, or the job below runs without the key).
A variable of type **File** works too: GitLab writes the value to a temporary
file and sets the variable to its path, which gplay accepts.

```yaml
# .gitlab-ci.yml
stages: [build, publish]

build:
  stage: build
  image: your-android-build-image # JDK + Android SDK
  script:
    - ./gradlew bundleRelease
  artifacts:
    paths:
      - app/build/outputs/bundle/release/app-release.aab

publish-internal:
  stage: publish
  image: alpine:3.20
  needs: [build]
  rules:
    - if: $CI_COMMIT_TAG
  script:
    - apk add --no-cache curl
    - curl -fsSL https://gplay.sh/install | GPLAY_VERSION=v2.3.1 GPLAY_INSTALL_DIR=/usr/local/bin sh # x-release-please-version
    - gplay auth doctor --package com.example.myapp
    - gplay releases upload app/build/outputs/bundle/release/app-release.aab --package com.example.myapp --track internal
```

### Bitrise

Add `GPLAY_SERVICE_ACCOUNT` in the workflow editor's **Secrets** tab and leave
*Expose for Pull Requests* off. Bitrise hands secrets to every step as
environment variables, and the `android-build` step exports the path of the AAB
it built as `BITRISE_AAB_PATH`.

```yaml
# bitrise.yml
format_version: "13"
default_step_lib_source: https://github.com/bitrise-io/bitrise-steplib.git

workflows:
  release:
    steps:
      - git-clone@8: {}
      - android-build@1:
          inputs:
            - variant: release
            - build_type: aab
      - script@1:
          title: Upload to the internal track
          inputs:
            - content: |-
                #!/usr/bin/env bash
                set -euo pipefail
                curl -fsSL https://gplay.sh/install | GPLAY_VERSION=v2.3.1 sh # x-release-please-version
                export PATH="$HOME/.local/bin:$PATH"
                gplay auth doctor --package com.example.myapp
                gplay releases upload "$BITRISE_AAB_PATH" --package com.example.myapp --track internal
```

### CircleCI

Add `GPLAY_SERVICE_ACCOUNT` under *Project Settings → Environment Variables*
(or in a context restricted to your release jobs), as the one-line JSON from
`jq -c`. The upload job uses the `machine` executor, which has a Docker
daemon, so it runs the published image instead of installing anything;
`-e GPLAY_SERVICE_ACCOUNT` forwards the variable without putting the key on the
command line.

```yaml
# .circleci/config.yml
version: 2.1

jobs:
  build:
    docker:
      - image: cimg/android:2026.08
    steps:
      - checkout
      - run: ./gradlew bundleRelease
      - persist_to_workspace:
          root: app/build/outputs/bundle/release
          paths: [app-release.aab]

  publish-internal:
    machine:
      image: ubuntu-2404:current
    environment:
      GPLAY_IMAGE: ghcr.io/pollyglot/gplay:v2.3.1 # x-release-please-version
    steps:
      - attach_workspace:
          at: .
      - run:
          name: Upload to the internal track
          command: |
            docker run --rm -e GPLAY_SERVICE_ACCOUNT "$GPLAY_IMAGE" \
              auth doctor --package com.example.myapp
            docker run --rm -e GPLAY_SERVICE_ACCOUNT -v "$PWD:/work" -w /work "$GPLAY_IMAGE" \
              releases upload app-release.aab --package com.example.myapp --track internal

workflows:
  release:
    jobs:
      - build
      - publish-internal:
          requires: [build]
```

### Jenkins

Store the key file as a **Secret file** credential (here with the id
`gplay-service-account`). The `file(...)` binding copies it to a temporary
file for the duration of the block, sets `GPLAY_SERVICE_ACCOUNT` to that path,
masks it in the log and deletes the file afterwards. Keep the `sh` script in
single quotes so the shell, not Groovy, expands the variables.

```groovy
// Jenkinsfile
pipeline {
  agent any
  environment {
    GPLAY_VERSION = 'v2.3.1' // x-release-please-version
  }
  stages {
    stage('Build') {
      steps {
        sh './gradlew bundleRelease'
      }
    }
    stage('Upload to internal') {
      when { buildingTag() }
      steps {
        withCredentials([file(credentialsId: 'gplay-service-account', variable: 'GPLAY_SERVICE_ACCOUNT')]) {
          sh '''
            set -eu
            curl -fsSL https://gplay.sh/install | sh
            export PATH="$HOME/.local/bin:$PATH"
            gplay auth doctor --package com.example.myapp
            gplay releases upload app/build/outputs/bundle/release/app-release.aab --package com.example.myapp --track internal
          '''
        }
      }
    }
  }
}
```

The install script reads `GPLAY_VERSION` from the pipeline's `environment`
block. The agent needs `curl`, `tar` and `sha256sum` or `shasum`.

## Bound and retry transient failures

Prefer the built-in `--retry` over a hand-rolled loop. It retries the
transient classes (transport errors, 5xx, 429 honouring `Retry-After`) with
exponential backoff, never retries non-transient 4xx or `edits.commit`, and
replays a non-idempotent write (an upload, a create, a refund) only when the
request provably never reached Google:

```bash
gplay releases upload app.aab --package com.example.myapp --track internal \
  --retry 3 --timeout 2m
```

`--timeout` bounds each request (60s default for control-plane calls; uploads
exempt unless set), so a hung connection fails in seconds instead of stalling
the job. With `--retry`, it's a per-attempt bound.

When you need shell-level control (retrying across *separate* commands, or
adding alerting), branch on the [exit code](/docs/concepts/exit-codes/)
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
   `releases rollout --track production --staged 0.05 --confirm`

Google rate-limits publishing; as a rule of thumb, don't publish to
alpha/beta more than once a day, and less often to production.

## Verify the install in CI

To gate the pipeline on artifact provenance, install from the release
archive and verify it before running anything:

```yaml
      - name: Install and verify gplay
        env:
          GH_TOKEN: ${{ github.token }}
          VERSION: v2.3.1 # x-release-please-version
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
