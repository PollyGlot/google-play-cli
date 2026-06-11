# Google Play CLI — Project Context

## What this project is

A CLI tool for the Google Play Developer API, in Go. Goal: replace
Fastlane/Ruby on Android CI pipelines and enable autonomous app
administration via CLI and AI agent skills.

Two repos:
1. **`gplay`** — the CLI (this repo).
2. **`google-play-cli-skills`** — agent skills (`SKILL.md` files). Live &
   public: https://github.com/PollyGlot/google-play-cli-skills

**Why Go:** one static binary, zero runtime deps, fast cold start (matters in
CI loops), trivial cross-platform distribution (Homebrew, `install.sh`).

**Prior art:** [Vacxe/google-play-cli](https://github.com/Vacxe/google-play-cli)
(Kotlin, wraps the official Java SDK, partial coverage) and
[GPC/yasserstudio](https://github.com/yasserstudio/gpc) (TypeScript, all 217
endpoints). Neither hits the "native Go + agent-first + companion skills" spot.

## Read these, in order

1. **`CONTEXT.md`** — glossary of canonical terms (`Edit`, `Account`, `Project`,
   …). **Use them verbatim** in code, comments, help. No synonyms.
2. **`docs/DESIGN.md`** — single source of truth for cross-command behavior
   (auth precedence, exit codes, output, verbosity, edit lifecycle).
3. **`docs/BACKLOG.md`** — out-of-scope surfaces; check before suggesting "add X".
4. **`docs/adr/`** — irreversible / surprising decisions with rationale.

## Authentication

Service Account + OAuth2: gplay reads a `service_account.json`, signs a JWT,
exchanges it for a short-lived access token, and uses that for all API calls.
Credential source via `GPLAY_SERVICE_ACCOUNT` (path or inline JSON) — full
resolution precedence in [`docs/DESIGN.md`](docs/DESIGN.md) §1.

## Google Play Developer API

- Base: `https://androidpublisher.googleapis.com/androidpublisher/v3`
  ([REST reference](https://developers.google.com/android-publisher/api-ref/rest)).
- **Edits model:** transactional `insert edit` → change → `commit edit`. gplay
  abstracts this in implicit mode; explicit mode allows batching.
- gplay speaks the API directly over HTTP from `internal/play/api/` — **not**
  via the official Go SDK. See [ADR-0007](docs/adr/0007-raw-http-not-google-go-sdk.md).
- Reporting API (vitals) is a **separate** service: `androidvitals.googleapis.com`.
- For "does method X exist / what's its request shape", **query** the offline
  Discovery snapshot — `grep docs/discovery/paths.txt`, `jq` the snapshot (see
  [`docs/discovery/README.md`](docs/discovery/README.md)). Never read it whole.

## CLI design principles

Full conventions in [`docs/DESIGN.md`](docs/DESIGN.md). Load-bearing ones:

- TTY-aware output: `table` interactive, `json` piped/CI; `--output table|json|markdown`.
- **`production` releases default to `draft`** (ADR-0002).
- **`--output json` is API pass-through** — mirrors the response shape verbatim (ADR-0003).
- **stdout = data, stderr = logs.** Always.
- No interactive prompts in CI; `--dry-run` on writes, `--confirm` for destructive ops.
- `--package` targeting, semantic exit codes.

## Shipped surface

Broad and growing — run `gplay --help` for the live tree (it's the source of
truth, not this file). Today: auth, apps (registry + details), releases
(upload/promote/rollout), tracks (list/view/create/availability), reviews,
metadata (listings + images), compliance datasafety, team (users + grants),
closed-track testers. Out-of-scope: [`docs/BACKLOG.md`](docs/BACKLOG.md);
planned-vs-shipped: [`docs/ROADMAP.md`](docs/ROADMAP.md).

**The CLI is self-documenting** — run `--help` (`gplay …`, `gplay releases
upload --help`) to confirm the exact interface before implementing or testing.
Do not memorize commands.

## Skills (companion repo)

One skill per shipped namespace plus a `gplay-cli-usage` foundation — roster
and `SKILL.md` contract fixed by [ADR-0021](docs/adr/0021-companion-skills-repo.md);
`gplay-vitals` and `gplay-subscription-management` gated until their surface
lands. Install: `npx skills add PollyGlot/google-play-cli-skills`.

## Build & test

```bash
make build       # → ./bin/gplay
make test        # go test ./... (RoundTripper-mocked, no network)
make lint        # golangci-lint run ./...
make format      # gofmt
make verb-gate   # fail if a pre-rename verb (ADR-0019) reappears
```

Tests **never** make outbound network calls. Mock pattern: a `testRoundTripper`
func (implements `http.RoundTripper`) injected via `option.WithHTTPClient(...)`;
each test wires the synthetic response it needs. No mock generation.

**Gate before every PR** (pre-commit hook + CI enforce it): `make format`,
`make lint`, `make test`.

## Adding a command

1. **In scope?** Cross-check `docs/BACKLOG.md` — surface the decision, don't
   silently promote a backlog item.
2. **Term check.** New domain noun → confirm/add in `CONTEXT.md`, no synonyms.
3. **Conventions.** Apply the relevant `docs/DESIGN.md` section; a deviation is
   its own documented decision.
4. **Test first**, RoundTripper-mocked; fixtures in `testdata/` past a few lines.
5. **Update `--help`** and command docs — long descriptions use `CONTEXT.md` terms.

## Repo layout

```
cmd/gplay/        ← entry point (supports `go install`)
commands/         ← one package per command group (auth, apps, releases, …)
internal/
  auth/           ← service account → OAuth2 token, keystore
  play/api/       ← raw HTTP client for the Developer API
  output/         ← table/json/markdown dispatcher
  config/         ← cascading config
docs/
  DESIGN.md       ← cross-command conventions
  BACKLOG.md      ← deferred API surfaces
  ROADMAP.md      ← planned-vs-shipped state
  CI_CD.md        ← wiring gplay into CI
  adr/            ← architecture decision records
  agents/         ← issue tracker, triage labels, domain workflow
CONTEXT.md        ← canonical domain glossary
```

## Notes

- Rate limits: don't publish more than once/day for alpha/beta, less for
  production.
- Do **not** add `google.golang.org/api/androidpublisher/v3` — gplay
  hand-rolls the HTTP on purpose ([ADR-0007](docs/adr/0007-raw-http-not-google-go-sdk.md)).
  For auth, **do** use `golang.org/x/oauth2/google` (ADR-0007 "What about auth?").
- Agent workflow docs (issue tracker, triage labels, domain) live in
  [`docs/agents/`](docs/agents/).
