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

## CLI design principles

TTY-aware output (`table` in TTY, `json` piped/CI; `--output table|json|markdown`),
no interactive prompts in CI, `--dry-run` on writes, `--confirm` for destructive
actions, `--package` targeting, semantic exit codes. Full conventions:
[`docs/DESIGN.md`](docs/DESIGN.md).

## Shipped surface

Broad and growing — run `gplay --help` for the live tree (it's the source of
truth, not this file). Today: auth, apps (registry + details), releases
(upload/promote/rollout), tracks (list/view/create/availability), reviews,
metadata (listings + images), compliance datasafety, team (users + grants),
closed-track testers. Out-of-scope: [`docs/BACKLOG.md`](docs/BACKLOG.md);
planned-vs-shipped: [`docs/ROADMAP.md`](docs/ROADMAP.md).

## Skills (companion repo)

One skill per shipped namespace plus a `gplay-cli-usage` foundation — roster
and `SKILL.md` contract fixed by [ADR-0021](docs/adr/0021-companion-skills-repo.md):
`gplay-cli-usage`, `gplay-setup`, `gplay-apps`, `gplay-release-flow`,
`gplay-tracks`, `gplay-reviews`, `gplay-metadata-sync`, `gplay-compliance`,
`gplay-team`. Gated until their surface lands: `gplay-vitals`
([#49](https://github.com/PollyGlot/google-play-cli/issues/49)),
`gplay-subscription-management` ([#51](https://github.com/PollyGlot/google-play-cli/issues/51)).
Install: `npx skills add PollyGlot/google-play-cli-skills`.

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
CONTEXT.md        ← canonical domain glossary
AGENTS.md         ← agent-specific instructions
```

## Agent workflow docs

- **Issue tracker** — GitHub Issues on `PollyGlot/google-play-cli`, four types
  via labels `type:prd|slice|arch|parking`. See
  [`docs/agents/issue-tracker.md`](docs/agents/issue-tracker.md) and
  [`docs/ROADMAP.md`](docs/ROADMAP.md).
- **Triage labels** — five canonical roles + local labels. See
  [`docs/agents/triage-labels.md`](docs/agents/triage-labels.md).
- **Domain** — single-context: `CONTEXT.md` + `docs/adr/`. See
  [`docs/agents/domain.md`](docs/agents/domain.md).

## Notes

- Rate limits: don't publish more than once/day for alpha/beta, less for
  production.
- Do **not** add `google.golang.org/api/androidpublisher/v3` — gplay
  hand-rolls the HTTP on purpose ([ADR-0007](docs/adr/0007-raw-http-not-google-go-sdk.md)).
  For auth, **do** use `golang.org/x/oauth2/google` (small, focused, no
  worthwhile hand-rolled equivalent — ADR-0007 "What about auth?").
