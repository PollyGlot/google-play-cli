# AGENTS.md

Unofficial, fast, lightweight CLI for the Google Play Developer API. Built in Go.
Designed to replace Fastlane/Ruby on Android CI pipelines and enable autonomous
app administration via CLI and AI agent skills.

## gplay skills

Agent Skills that teach an AI agent to drive `gplay` live in the sibling repo
`PollyGlot/google-play-cli-skills`. The roster, naming, and `SKILL.md` contract
are fixed by [ADR-0021](docs/adr/0021-companion-skills-repo.md): one skill per
shipped namespace (`gplay-setup`, `gplay-apps`, `gplay-release-flow`,
`gplay-tracks`, `gplay-reviews`, `gplay-metadata-sync`, `gplay-compliance`,
`gplay-team`) plus a `gplay-cli-usage` foundation; vitals and subscriptions are
gated until their surfaces land. Bootstrap tracked in
[#53](https://github.com/PollyGlot/google-play-cli/issues/53). Not yet published.

## Project status

Pre-1.0. The MVP surface is auth, `apps`, `releases upload`,
`tracks list/promote/rollout/halt/resume/complete`, and `reviews list/reply`.
Everything outside that scope is in [`docs/BACKLOG.md`](docs/BACKLOG.md) —
**do not silently add features from the backlog**; if a backlog item suddenly
matters, surface it as a separate decision first.

## Reading the docs (in this order)

1. [`CLAUDE.md`](CLAUDE.md) — high-level project context, command roadmap.
2. [`CONTEXT.md`](CONTEXT.md) — glossary of canonical terms (`Edit`, `Account`,
   `Project`, ...). **Use these terms verbatim** in code, comments, and CLI
   help. Do not invent synonyms.
3. [`docs/DESIGN.md`](docs/DESIGN.md) — CLI conventions (auth precedence, exit
   codes, output formats, verbosity, edit lifecycle). **The single source of
   truth for cross-command behavior.**
4. [`docs/BACKLOG.md`](docs/BACKLOG.md) — out-of-scope features. Refer here
   before suggesting "we should also add X".
5. [`docs/adr/`](docs/adr/) — irreversible / surprising decisions with rationale.

## Core principles

(Full list in [`docs/DESIGN.md`](docs/DESIGN.md). Highlights below.)

- **Explicit flags.** Long-form flags in docs/tests/examples (`--package`,
  `--track`, `--output`). Short flags only for very common ones.
- **TTY-aware defaults.** `table` in interactive terminals, `json` for pipes
  and CI. Override with `--output`.
- **Safe production defaults.** Releases on the `production` track default to
  `draft`. See [ADR-0002](docs/adr/0002-safe-production-defaults.md).
- **No interactive prompts.** Destructive operations use `--confirm`.
- **`--output json` is pass-through.** Mirrors the Google Play Developer API
  response shape verbatim. See [ADR-0003](docs/adr/0003-json-passthrough.md).
- **stdout = data, stderr = logs.** Always.

## Discovering commands

**Before implementing or testing any command, run `--help` to confirm the
exact interface.** The CLI is self-documenting:

```bash
gplay --help                       # List all commands
gplay releases --help              # List releases subcommands
gplay releases upload --help       # Show all flags for a command
```

Do not memorize commands. Always check `--help` for the current interface.

## Documentation — Google Play Developer API

Primary reference: <https://developers.google.com/android-publisher/api-ref/rest>.

For endpoint existence and request/response schemas, prefer the **offline
Discovery snapshot** at `docs/discovery/androidpublisher_v3.json` once it
exists (regenerate via the make target documented in
`docs/discovery/README.md`). The Google Discovery document is the canonical
machine-readable schema for the v3 API.

Notes:
- The Reporting API (vitals) is a **separate** service —
  `androidvitals.googleapis.com` — with its own Discovery doc and scope. Do
  not conflate the two when looking up endpoints.
- Validate flags against the **request** schema for the specific method;
  create and update payloads often differ.

## Build & test

(Aspirational — `Makefile` will land with the initial scaffold.)

```bash
make build           # Build binary into ./bin/gplay
make test            # Run all tests (RoundTripper-mocked, no network)
make test-e2e        # E2E tests against a real test app (requires GPLAY_E2E_SA env)
make lint            # golangci-lint
make format          # gofmt + goimports
make install-hooks   # Local pre-commit hook
```

Tests must not make outbound network calls except in `test-e2e`. The mock
pattern is `http.Client{Transport: testRoundTripper(...)}` injected via
`option.WithHTTPClient(...)` when constructing the Google Play client. A
`testRoundTripper` is a function type implementing `http.RoundTripper`:
each test wires up the function that returns the synthetic response it
needs. No mock generation, no wrapper interface — just the standard Go
HTTP transport boundary.

## Adding a new command

1. **Check it's in scope.** Cross-reference with `docs/BACKLOG.md`. If the
   feature is in the backlog, surface the decision rather than silently
   promoting it.
2. **Cross-check the term.** If the command involves a new domain noun,
   confirm against `CONTEXT.md`. Add a glossary entry if it's a new canonical
   term; do not invent synonyms.
3. **Apply the design conventions.** Read the relevant section of
   `docs/DESIGN.md` (auth, output, exit codes, edit lifecycle, ...). If the
   new command would deviate, that deviation is its own decision — document
   it.
4. **Write the test first.** RoundTripper-mocked. Fixtures in
   `testdata/` if they exceed a few lines.
5. **Update `--help` and command docs.** Long descriptions reference
   `CONTEXT.md` terms verbatim.

## PR guardrails

Minimum gate before opening a PR:

- `make format`
- `make lint`
- `make test`

Pre-commit hook (`make install-hooks`) runs the same locally.

CI must enforce `format` + `lint` + `test` on every PR and on `main`.

## Authentication note for tests and live runs

Tests **never** read live credentials. The mock RoundTripper short-circuits
all API calls. For live exploratory runs (not in CI), point at a throwaway
Google Cloud project + a throwaway Play Console app, and never mutate
production apps with the test service account.

Once a throwaway test app exists for `gplay`, document its package name
here so contributors and agents know what's safe to mutate.
