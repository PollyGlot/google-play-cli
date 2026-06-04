# AGENTS.md

Unofficial, fast, lightweight CLI for the Google Play Developer API, in Go.
Replaces Fastlane/Ruby on Android CI and enables autonomous app administration
via CLI and AI agent skills.

## gplay skills

Skills that teach an agent to drive `gplay` live in the sibling repo
`PollyGlot/google-play-cli-skills` — one per shipped namespace plus a
`gplay-cli-usage` foundation, roster fixed by
[ADR-0021](docs/adr/0021-companion-skills-repo.md) (`gplay-setup`, `gplay-apps`,
`gplay-release-flow`, `gplay-tracks`, `gplay-reviews`, `gplay-metadata-sync`,
`gplay-compliance`, `gplay-team`; vitals & subscriptions gated until their
surface lands). Install: `npx skills add PollyGlot/google-play-cli-skills`.

## Project status

Pre-1.0, surface already broad: auth, `apps` (registry + details), `releases`
(upload/promote/rollout/halt/resume/complete), `tracks`
(list/view/create/availability), `reviews` (list/reply), `metadata`
(listings + images), `compliance datasafety`, `team` (users + grants),
closed-track `testers`. Run `gplay --help` for the live tree. Anything outside
it is in [`docs/BACKLOG.md`](docs/BACKLOG.md) — **do not silently add backlog
features**; if a backlog item suddenly matters, surface it as a decision first.

## Reading the docs (in order)

1. [`CLAUDE.md`](CLAUDE.md) — project context.
2. [`CONTEXT.md`](CONTEXT.md) — glossary of canonical terms (`Edit`, `Account`,
   `Project`, ...). **Use these verbatim** in code, comments, help. No synonyms.
3. [`docs/DESIGN.md`](docs/DESIGN.md) — cross-command conventions (auth
   precedence, exit codes, output, verbosity, edit lifecycle). **Single source
   of truth for cross-command behavior.**
4. [`docs/BACKLOG.md`](docs/BACKLOG.md) — out-of-scope; check before suggesting
   "we should also add X".
5. [`docs/adr/`](docs/adr/) — irreversible / surprising decisions with rationale.

## Core principles

Full list in [`docs/DESIGN.md`](docs/DESIGN.md). The load-bearing ones:

- **Safe production defaults** — `production` releases default to `draft`
  ([ADR-0002](docs/adr/0002-safe-production-defaults.md)).
- **`--output json` is pass-through** — mirrors the API response shape verbatim
  ([ADR-0003](docs/adr/0003-json-passthrough.md)). Default is TTY-aware (`table`
  interactive, `json` piped/CI).
- **stdout = data, stderr = logs.** Always.
- Explicit long-form flags in docs/tests/examples; no interactive prompts
  (`--confirm` for destructive ops).

## Discovering commands

**Before implementing or testing a command, run `--help` to confirm the exact
interface** — the CLI is self-documenting (`gplay --help`, `gplay releases
--help`, `gplay releases upload --help`). Do not memorize commands.

## Google Play Developer API

Primary reference: <https://developers.google.com/android-publisher/api-ref/rest>.
For endpoint/schema lookups, prefer the offline Discovery snapshot at
`docs/discovery/androidpublisher_v3.json` **once it exists**
([#52](https://github.com/PollyGlot/google-play-cli/issues/52)) — Google's
Discovery doc is the canonical machine-readable v3 schema. Notes:

- The Reporting API (vitals) is a **separate** service
  (`androidvitals.googleapis.com`) with its own Discovery doc — don't conflate.
- Validate flags against the **request** schema of the specific method; create
  and update payloads often differ.

## Build & test

```bash
make build           # → ./bin/gplay
make test            # go test ./... (RoundTripper-mocked, no network)
make lint            # golangci-lint run ./...
make format          # gofmt
make verb-gate       # fail if a pre-rename verb (ADR-0019) reappears
make install-hooks   # local pre-commit hook
```

Tests **never** make outbound network calls. Mock pattern:
`http.Client{Transport: testRoundTripper(...)}` injected via
`option.WithHTTPClient(...)` when constructing the client. `testRoundTripper`
is a func implementing `http.RoundTripper` — each test wires the synthetic
response it needs. No mock generation, no wrapper interface — just the standard
Go HTTP transport boundary.

## Adding a new command

1. **In scope?** Cross-reference `docs/BACKLOG.md`; surface the decision rather
   than silently promoting a backlog item.
2. **Term check.** New domain noun → confirm/add in `CONTEXT.md`, no synonyms.
3. **Conventions.** Apply the relevant `docs/DESIGN.md` section; a deviation is
   its own documented decision.
4. **Test first.** RoundTripper-mocked; fixtures in `testdata/` past a few lines.
5. **Update `--help`** and command docs — long descriptions use `CONTEXT.md`
   terms verbatim.

## PR guardrails

Gate before opening a PR (the pre-commit hook runs the same locally, CI enforces
on every PR and `main`): `make format`, `make lint`, `make test`.

## Auth for tests and live runs

Tests never read live credentials — the mock RoundTripper short-circuits all API
calls. For live exploratory runs (not CI), point at a throwaway Cloud project +
throwaway Play Console app; never mutate production apps with the test service
account. Once a throwaway test app exists, document its package name here.
