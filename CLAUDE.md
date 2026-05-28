# Google Play CLI — Project Context

## What this project is

A CLI tool for the Google Play Developer API. The goal is to replace
Fastlane/Ruby on Android CI pipelines and enable autonomous app
administration via CLI and AI agent skills.

Two repos planned:
1. **`gplay`** — the CLI itself (this repo)
2. **`google-play-cli-skills`** — agent skills (SKILL.md files) for AI
   agents like Claude Code

## Existing alternatives we looked at

- https://github.com/Vacxe/google-play-cli — **Kotlin** wrapper around
  the official Google Play Java library; partial API coverage.
- GPC by yasserstudio (https://github.com/yasserstudio/gpc) —
  TypeScript, covers all 217 API endpoints, ships **both** as
  `npm install` and as a standalone binary via an install script.

Neither covers the "native Go cold start + MVP-scoped + agent-first
design + companion skills" sweet spot that this project aims for.

## Language & stack

**Go**:
- Standalone binary, zero runtime dependencies for end users
- Easy cross-platform distribution via Homebrew and `install.sh`
- Fast cold start (critical in CI loops)
- Single static binary fits container-based CI images perfectly

## Authentication

Google Play uses **Service Account + OAuth2**:
1. User creates a Service Account in Google Cloud Console
2. Downloads a `service_account.json`
3. CLI reads that JSON, signs a JWT, exchanges it for a short-lived
   OAuth2 access token
4. Uses that token for all API calls

Environment variable: `GPLAY_SERVICE_ACCOUNT` (path to JSON or JSON
content directly).

## Google Play Developer API

- Base URL: `https://androidpublisher.googleapis.com/androidpublisher/v3`
- Docs: https://developers.google.com/android-publisher
- REST reference: https://developers.google.com/android-publisher/api-ref/rest
- Key concept: **Edits model** — transactional workflow: `insert edit` →
  make changes → `commit edit`
- gplay speaks the API directly over HTTP from `internal/play/api/`. We
  do **not** depend on `google.golang.org/api/androidpublisher/v3` (the
  auto-generated official Go SDK). See
  [`docs/adr/0007-raw-http-not-google-go-sdk.md`](docs/adr/0007-raw-http-not-google-go-sdk.md)
  for the why.
- Reporting API (vitals) lives on a **separate** service:
  `androidvitals.googleapis.com`

## CLI design principles

- **JSON-first output**: `table` default in TTY, `json` when piped/CI
- **No interactive prompts** in CI mode
- **`--dry-run`** on all write operations
- **`--output`** flag: `table`, `json`, `markdown`
- **`--package`** flag: Android package name (e.g. `com.example.myapp`)
- TTY-aware (detect if stdout is a terminal)
- Semantic exit codes (see `docs/DESIGN.md` for the full table)
- `--confirm` for destructive actions

Full conventions live in [`docs/DESIGN.md`](docs/DESIGN.md).

## Planned command structure

```
gplay auth login --service-account /path/to/service_account.json
gplay auth status
gplay auth doctor

gplay apps list
gplay apps info --package com.example.myapp

gplay releases upload app.aab --package com.example.myapp --track internal
gplay releases list --package com.example.myapp --track production
gplay releases promote --package com.example.myapp --from internal --to alpha
gplay releases rollout --package com.example.myapp --track production --to 0.10

gplay tracks list --package com.example.myapp
gplay tracks status --package com.example.myapp --track production

gplay reviews list --package com.example.myapp --stars 1-2
gplay reviews reply --review-id REVIEW_ID --reply "Thank you..."

gplay vitals crashes --package com.example.myapp --version 142
gplay vitals anr --package com.example.myapp

gplay metadata list --package com.example.myapp
gplay metadata apply --package com.example.myapp --dir ./metadata --dry-run

gplay subscriptions list --package com.example.myapp
gplay iap list --package com.example.myapp
```

Scope today is the strict MVP (auth, apps, releases upload, tracks,
reviews) — the rest lives in [`docs/BACKLOG.md`](docs/BACKLOG.md).

## Skills structure (second repo)

Each skill = a folder with a `SKILL.md` (markdown instructions for AI
agents):

```
skills/
  gplay-release-flow/
    SKILL.md
  gplay-reviews/
    SKILL.md
  gplay-vitals/
    SKILL.md
  gplay-metadata-sync/
    SKILL.md
  gplay-track-management/
    SKILL.md
  gplay-subscription-management/
    SKILL.md
```

Install pattern:
```bash
npx skills add <username>/google-play-cli-skills
```

## Project structure (CLI repo)

```
google-play-cli/
  go.mod
  Makefile
  CLAUDE.md              ← this file
  AGENTS.md              ← agent-specific instructions
  CONTEXT.md             ← canonical domain glossary
  cmd/
    gplay/
      main.go            ← entry point (supports `go install`)
  commands/
    auth/
    apps/
    releases/
    tracks/
    reviews/
    vitals/
    metadata/
    subscriptions/
  internal/
    auth/                ← OAuth2 service account logic
    client/              ← HTTP client wrapper
    output/              ← table/json/markdown formatters
    config/              ← config file management
  docs/
    DESIGN.md            ← cross-command CLI conventions
    BACKLOG.md           ← deferred API surfaces
    CI_CD.md             ← how to wire gplay into a CI pipeline
    adr/                 ← architecture decision records
```

## Priority order for implementation

1. Auth (service account → OAuth2 token)
2. `apps list` (smoke test that auth works — backed by local registry)
3. `releases upload` (core CI use case, replaces Fastlane supply)
4. `tracks list / promote` (release management)
5. `reviews list / reply`

Anything beyond #5 is explicitly deferred (see `docs/BACKLOG.md`).

## Agent skills

### Issue tracker

Issues vivent dans GitHub Issues sur `PollyGlot/google-play-cli`. Quatre
types co-existent via labels `type:prd` / `type:slice` / `type:arch` /
`type:parking`. Voir [`docs/agents/issue-tracker.md`](docs/agents/issue-tracker.md)
et [`docs/ROADMAP.md`](docs/ROADMAP.md) pour la vue d'ensemble.

### Triage labels

Mapping des cinq rôles canoniques + labels locaux (`type:*`, `area:*`,
`priority:*`). Voir [`docs/agents/triage-labels.md`](docs/agents/triage-labels.md).

### Domain docs

Single-context : `CONTEXT.md` à la racine + `docs/adr/`. Voir
[`docs/agents/domain.md`](docs/agents/domain.md).

## Notes

- The Edits model means most publishing operations need 3 steps: create
  edit, make changes, commit edit. The CLI abstracts this transparently
  in implicit mode; explicit mode is available for batching.
- Rate limits: don't publish more than once per day for alpha/beta, less
  for production.
- Do **not** add `google.golang.org/api/androidpublisher/v3` as a
  dependency. gplay hand-rolls the HTTP calls in `internal/play/api/`
  on purpose — see
  [`docs/adr/0007-raw-http-not-google-go-sdk.md`](docs/adr/0007-raw-http-not-google-go-sdk.md).
- For auth, do use `golang.org/x/oauth2/google` (small, focused, has no
  equivalent worth hand-rolling — see ADR-0007 "What about auth?").
