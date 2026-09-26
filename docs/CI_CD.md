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
- `70` → a check command (`apps audit`) ran fine and **found** drift; the report
  on stdout is complete, act on what it names

> An exit 30 or 60 carrying the `editAlreadyExists` reason is the orphaned-Edit
> case — a stale Edit left open by a hard-killed run. Don't blind-retry it; see
> [§7 Troubleshooting: orphaned Edits](#7-troubleshooting-orphaned-edits-editalreadyexists).

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

Fully detailed migration table: parked (issue #526), to be added once real
migrators give feedback on the pitfalls.

## 7. Troubleshooting: orphaned Edits (`editAlreadyExists`)

Every mutating Play command runs inside an **Edit** — a transaction gplay opens
(`edits.insert`), changes, and commits implicitly. On any normal failure gplay
auto-discards the open Edit before returning, so nothing is left behind. But a
**hard kill** — `SIGKILL`, an OOM, a CI runner eviction or job-timeout — between
insert and commit kills gplay *before* its cleanup can run, leaving an
**orphaned Edit open on the Play side**. In-process cleanup cannot cover a hard
kill, by definition.

### What you'll see

The next mutating run's `edits.insert` is rejected because an Edit is already
open. gplay surfaces it with the discriminating `editAlreadyExists` reason and a
message naming the remediation:

```text
gplay: an Edit is already open on com.example.myapp (wait ~24h for it to expire,
or release it via the Google Play Console): edits.insert on com.example.myapp:
... [reason: editAlreadyExists]
```

Exit code follows the upstream status (see the [exit-code table](DESIGN.md#9-exit-codes)):

- **exit 30** — the usual case (`400` + `editAlreadyExists`): API misuse,
  recoverable.
- **exit 60** — when Google ships the reason on a rate-limited response
  (`429` + `editAlreadyExists`): state conflict.

Either way the cause is the same orphaned Edit, and **retrying immediately will
keep failing** — don't put this behind a blind retry loop.

### How to recover (with today's command surface)

There is no gplay command to discard an orphaned Edit yet (that's the parked
explicit-edits mode, [#48](https://github.com/PollyGlot/google-play-cli/issues/48)).
Two recovery paths exist today:

1. **Wait for Play-side expiry.** An open Edit auto-expires after **~24h**.
   After that, the next run's `edits.insert` succeeds with no intervention. Best
   when the pipeline is not time-critical.
2. **Release it via the Google Play Console (immediate).** Open the app in the
   Play Console; a stale/pending Edit can be discarded there, after which
   re-running gplay succeeds right away.

Confirm access is otherwise healthy with
`gplay auth doctor --package <your.package>` — it opens and discards a throwaway
Edit, so once the orphan is gone it round-trips cleanly.

### In a pipeline, meanwhile

- **Branch on the exit code, don't blind-retry.** Treat exit 30 / 60 with an
  `editAlreadyExists` reason as "needs the orphan cleared", not "retry now". The
  JSON error envelope (`--output json`) exposes `reasons: ["editAlreadyExists"]`
  on stdout so an agent can detect it precisely.
- **Don't use `--keep-edit-on-failure` in CI.** That flag *intentionally* keeps
  the Edit open on failure (for local debugging) and reports its ID; in a
  pipeline it manufactures exactly this orphaned-Edit situation.
- **Prevent it where you can:** give jobs a generous step timeout so the runner
  doesn't evict gplay mid-commit, and avoid `kill -9` on the process.

The structural fix — explicit `edits begin/commit/discard` so a pipeline can
adopt and discard an Edit by ID — is tracked in
[#48](https://github.com/PollyGlot/google-play-cli/issues/48) and intentionally
parked; this runbook covers recovery with the commands that exist today.

## 8. gplay's own CI (for repository maintainers)

> Everything above is about wiring **your** app's pipeline. This last section
> documents how the **gplay repository itself** is tested — relevant only if
> you're contributing to gplay, not to using the CLI.

The pipeline lives in [`.github/workflows/`](../.github/workflows/); every
third-party action is SHA-pinned (see
[`CONTRIBUTING.md`](../CONTRIBUTING.md#github-actions-are-sha-pinned)).

| Workflow | Trigger | What it does |
|---|---|---|
| `ci.yml`: **Build, lint, test** | PR + push to `main` | aggregator over the `lint` job (gofmt, `go vet`, golangci-lint, build) and the `test` shards (`go test -race`, split by package). **Required check.** |
| `test-uncached.yml` | daily + manual | `go test -race -count=1 ./...` with no cache, the safety net for the cached test results. Not required. |
| `ci.yml` — **Docs sanity** | PR + push to `main` | verb-gate (ADR-0019), shellcheck, required-files. **Required check.** |
| `ci.yml` — **Fuzz smoke** | PR + push to `main` | bounded fuzzing of the untrusted-input parsers. Not required. |
| `codeql.yml` | PR + push to `main` + weekly | CodeQL `security-and-quality` static analysis of our own Go. Not required (yet). |
| `govulncheck.yml` | weekly + `go.mod`/`go.sum` push | dependency-vulnerability scan. |
| `release-rehearsal.yml` | PR touching release machinery | non-publishing GoReleaser dry run. Not required. |

### Path-based job gating

A leading **`changes`** job ([`dorny/paths-filter`](https://github.com/dorny/paths-filter))
classifies each diff and exposes a `code` output. A change is `code: true` if it
touches any of `cmd/**`, `commands/**`, `internal/**`, `**/*.go`, `go.mod`,
`go.sum`, `Makefile`, `.github/**`, `scripts/**`, `install.sh`, or
`docs/discovery/**` — the same "not docs-only" boundary as
[`CLAUDE.md`](../CLAUDE.md). Everything else (Markdown, the rest of `docs/**`,
`website/**`, doc assets) is docs/site-only.

Two entries in that list are easy to get wrong, and both were:

- **The Go source directories are matched wholesale, not by `*.go` extension.**
  The binary embeds non-Go files — `internal/schemaindex/schema_index.json` and
  `internal/compliance/datasafety/reference.csv`, both `go:embed` — so an
  extension-based filter classified a snapshot refresh as docs-only and skipped
  the tests that guard it (notably `internal/schemaindex/integrity_test.go`,
  which asserts the committed snapshots derive byte-identically to the embedded
  index). Directory globs keep any future `go:embed` covered by default.
- **`docs/discovery/**` is code, despite living under `docs/`.** Those snapshots
  are the inputs to `make schema-index-update`, so changing them can
  desynchronise the embedded Schema index even when nothing under `internal/`
  moves.

The rule of thumb: `code` means *"can this change the built binary?"*, not
*"does this end in `.go`?"*.

On a docs-only PR the heavy jobs short-circuit, so the frequent self-merged doc
PRs don't pay the Go jobs (lint, test shards, fuzz). **The required-check interplay is
the subtle part:**

- **`fuzz`** is *not* required, so it skips outright at the job level:
  `if: needs.changes.outputs.code == 'true'`.
- **`lint`** and **`test`** are *not* required either, so they skip at the job
  level on docs-only PRs too.
- **`build`** ("Build, lint, test") *is* required. GitHub treats a **skipped**
  required job as unsatisfied: it would block merge forever. So `build` runs
  with `if: ${{ !cancelled() }}`: it runs whatever happened upstream, failures
  included, and skips only when the run itself is cancelled (see
  [Concurrency](#concurrency-prs-cancel-main-never-does)). It derives
  its verdict from `needs.*.result` (see the next section). On a docs-only PR it
  finds `code` false and goes green in seconds, leaving the required check
  satisfied without running any Go tooling.
- **`docs`** ("Docs sanity") always runs — it's required, cheap, and relevant to
  every PR.

Net effect: docs-only PRs get a fast green pipeline; any touch to a Go source
directory, `go.mod`, `Makefile`, `.github`, `scripts`, or the Discovery
snapshots flips `code` true and runs the full pipeline unchanged — gating is by
changed path, never by trust, so there's no loss of safety.

When adding a path that the build consumes, add it to the filter in the same PR.
A green "Build, lint, test" that finished in seconds on a code PR cannot happen
any more: the aggregator is red unless `lint` and every `test` shard succeeded.
On a docs-only PR its log says `docs/site-only change: lint and tests skipped by
design`.

### Build, lint, test: parallel jobs and a living Go cache

The required check used to be one serial job, about 7.5 minutes of which
`go test -race ./...` took 6.5: most of that is compiling and linking one race
test binary per package, not running tests. Two levers shorten it without
dropping a single check.

**Parallel jobs.** `lint` (gofmt, `go vet`, golangci-lint, `go build`) runs
beside the `test` matrix, and `go test -race` is split into shards. Packages
are dealt round-robin over `go list ./...`, so every package lands in exactly
one shard and a new package needs no CI edit; a guard fails the shard if the
dealt list differs from `go list ./...`. The few packages whose tests run far
longer than the rest (`internal/artifact` alone runs for about 80 s) are dealt
first, one per shard, so they never stack up on one runner. The shard count is
the length of the `matrix.shard` list; the script reads it back from
`strategy.job-total`. Runners are free on this public repo, so extra jobs cost
nothing.

**The aggregator keeps the required name.** The job named "Build, lint, test"
now only aggregates: `needs: [changes, lint, test]`, `if: ${{ !cancelled() }}`, one step
that reads the results. It is red when:

- `changes` did not succeed (an empty `code` output must not read as
  docs-only);
- `code` is true and `lint` is anything but `success`;
- `code` is true and `test` is anything but `success`. For a matrix job,
  `needs.test.result` is the result of the matrix as a whole, so one failed or
  cancelled shard makes it `failure`/`cancelled`. `fail-fast: false` lets the
  other shards finish so every red shard is visible.

**A living cache.** `setup-go`'s built-in cache is keyed on `go.sum` and is
never rewritten once that key exists, so it only ever holds dependencies: the
project's own packages were recompiled and every test rerun on every run. The
`lint` job and the `test` shards instead cache `GOCACHE` and `GOMODCACHE`
explicitly (`actions/cache/restore` and `actions/cache/save`), one entry per job
and per `main` commit (`go-lint-<os>-go<version>-<sha>`,
`go-test-<os>-go<version>-shard<i>of<n>-<sha>`), restored by prefix. Only pushes to `main` save; pull requests only read, so no PR can feed
the cache another PR reads. With the build cache warm, Go also reuses **test
results**: a package whose sources, dependencies, and test inputs are unchanged
prints `(cached)` instead of running again. The cache is content-addressed, so a
changed input always invalidates the result; the one thing it can hide is a
flaky test in a package nobody touched. Packages whose tests read files from the checkout
(`internal/apiregistry`, `internal/discovery`, `internal/schemaindex`, ...) rerun
every time anyway: Go keys those inputs on mtime, and `actions/checkout` sets a
fresh one. Each `main` commit adds about 300 MB of cache entries (seven jobs of
about 40 MB); GitHub's 10 GB per-repo quota evicts the oldest, and only the
latest is ever restored.

**The safety net.** `test-uncached.yml` runs `go test -race -count=1 ./...`
every night (and on demand) with no cache at all, so a flaky test surfaces
within a day even when no PR touches its package. Like the other scheduled
workflows (CodeQL, govulncheck, Discovery Watch) it is not a check on any PR; a
failure is reported by GitHub's scheduled-workflow notification and shows in
the Actions tab.

### Concurrency: PRs cancel, main never does

`ci.yml` and `codeql.yml` share one concurrency rule:

```yaml
group: ci-${{ github.workflow }}-${{ github.event_name == 'pull_request' && github.ref || github.run_id }}
cancel-in-progress: ${{ github.event_name == 'pull_request' }}
```

On a pull request a new push supersedes the old head, so the old run is
cancelled. Every other run (a push to `main`, the CodeQL schedule) gets a group
of its own and runs to the end. The group used to be keyed on the ref for pushes
too, and back-to-back merges cancelled each other: over 60 days 28% of `main`
CI runs never gave a verdict nor saved the living cache, and when #547 and #548
landed 25 s apart the cancelled run hid whether #547 alone was sound. The
per-run group matters on its own: with a shared group and
`cancel-in-progress: false`, GitHub still cancels the *pending* run when a third
one queues, so even a per-sha group could drop a run when a push and the
schedule share a commit.

A cancelled PR run skips the aggregator (`!cancelled()`) rather than failing
it, so a superseded head no longer shows a red "Build, lint, test". This stays
fail-closed: the newer run reports the check, and a head whose only run was
cancelled has no required check at all, which blocks merge.

### Merging: `scripts/merge-pr.sh`

The ruleset asks for an approving review and up-to-date required checks. With a
single maintainer no approval can exist, so PRs merge with
`gh pr merge --admin`, and `--admin` skips the up-to-date rule too: #547 and
#548 each passed CI against an older `main`, and their squashes together broke
`TestCoverageDocMatchesSources`. The repo is owned by a user account, so GitHub's
merge queue is not available. `scripts/merge-pr.sh <n>` puts the rule back in
front of the admin merge: it refuses when the PR head does not contain the
current `origin/main`, or when a required check (read from the branch rules) is
missing, pending, or red, and otherwise runs
`gh pr merge <n> --admin --squash --match-head-commit <sha>`. `--dry-run` runs
every gate without merging. A branch that is behind is brought up to date with
`git merge origin/main` (or `gh pr update-branch <n>`), never a rebase: the
squash makes the merge commit free.


### Release rehearsal

A release config is otherwise only exercised once a tag exists, i.e. mid-release,
when a mistake costs a half-published version. `release-rehearsal.yml` runs the
same GoReleaser config in dry run on the PR that changes it: `goreleaser check`
(advisory) then `release --snapshot --clean --skip=publish,sign,sbom,announce`,
the same flags as `make release-snapshot`. Nothing is published: `--snapshot`
plus the skip list, `permissions: contents: read`, and no secret reaches the job
(the tap token is a placeholder string, present only so the Homebrew template
renders).

It triggers on `.goreleaser.yaml`, the release workflows, `install.sh` and
`Makefile`, so ordinary code PRs don't pay it, and it is **not** a required
check for exactly that reason: a check that never runs on most PRs would block
merge if required.

`goreleaser check` is `continue-on-error` because it also exits non-zero on
deprecations, and `brews:` is deprecated in favour of `homebrew_casks:`.
Migrating changes how users install gplay, so it is a product decision rather
than something CI should force; the snapshot build is the blocking gate.

### CodeQL

`codeql.yml` runs GitHub's CodeQL taint-tracking over gplay's own Go on every PR,
every push to `main`, and a weekly cron. gplay handles service-account
credentials, JWTs, and untrusted API responses, so the queries that matter here
are credential leakage to logs, injection, and path traversal. It runs the
broad `security-and-quality` suite and is intentionally **non-required** at
first, so we can watch the alert volume before promoting it to a required check.
Triage each alert (fix, or dismiss with a reason); the baseline is zero open
alerts. Findings surface in the repo's **Security → Code scanning** tab.
