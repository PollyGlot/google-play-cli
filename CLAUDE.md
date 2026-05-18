# Google Play CLI — Project Context

## What this project is

A CLI tool for the Google Play Developer API, inspired by [`asc`](https://github.com/rorkai/App-Store-Connect-CLI) (App Store Connect CLI by rorkai). The goal is to replace Fastlane/Ruby on Android CI pipelines and enable autonomous app administration via CLI and AI agent skills.

Two repos planned:
1. **`gplay`** — the CLI itself
2. **`google-play-cli-skills`** — agent skills (SKILL.md files) for AI agents like Claude Code

## Reference projects

- CLI inspiration: https://github.com/rorkai/App-Store-Connect-CLI (Go, 4.1k stars)
- Skills inspiration: https://github.com/rorkai/app-store-connect-cli-skills
- Existing Go alternative (partial): https://github.com/Vacxe/google-play-cli
- Existing TypeScript alternative (full but heavy): GPC by yasserstudio

## Language & stack

**Go** — same rationale as `asc`:
- Standalone binary, zero dependencies for end users
- Easy distribution via Homebrew and install script
- Fast cold start
- Great for CI pipelines

## Authentication

Google Play uses **Service Account + OAuth2** (different from Apple's JWT):
1. User creates a Service Account in Google Cloud Console
2. Downloads a `service_account.json`
3. CLI reads that JSON, signs a JWT, exchanges it for a short-lived OAuth2 access token
4. Uses that token for all API calls

Environment variable: `GPLAY_SERVICE_ACCOUNT` (path to JSON or JSON content directly)

## Google Play Developer API

- Base URL: `https://androidpublisher.googleapis.com/androidpublisher/v3`
- Docs: https://developers.google.com/android-publisher
- REST reference: https://developers.google.com/android-publisher/api-ref/rest
- Key concept: **Edits model** — transactional workflow: `insert edit` → make changes → `commit edit`
- Official Go client lib available: `google.golang.org/api/androidpublisher/v3`

## CLI design principles (follow `asc` conventions)

- **JSON-first output**: default `table` in TTY, `json` when piped/CI
- **No interactive prompts** in CI mode
- **`--dry-run`** on all write operations
- **`--output`** flag: `table`, `json`, `markdown`
- **`--package`** flag: Android package name (e.g. `com.example.myapp`)
- TTY-aware (detect if stdout is a terminal)
- Semantic exit codes
- `--confirm` for destructive actions

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
gplay releases rollout --package com.example.myapp --track production --percentage 10

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

## Skills structure (second repo)

Each skill = a folder with a `SKILL.md` (markdown instructions for AI agents):

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

Install pattern (same as asc-skills):
```bash
npx skills add <username>/google-play-cli-skills
```

## Project structure (CLI repo)

```
google-play-cli/
  main.go
  go.mod
  CLAUDE.md              ← this file
  AGENTS.md              ← agent-specific instructions
  cmd/
    root.go
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
    COMMANDS.md
    CI_CD.md
```

## Priority order for implementation

1. Auth (service account → OAuth2 token)
2. `apps list` (smoke test that auth works)
3. `releases upload` (core CI use case, replaces Fastlane supply)
4. `tracks list / promote` (release management)
5. `reviews list / reply`
6. `vitals crashes / anr`
7. `metadata apply`

## Key differences vs Apple/asc

| | asc (Apple) | gplay (Google) |
|---|---|---|
| Auth | JWT (direct) | Service Account → OAuth2 |
| API model | REST, immediate | REST, **edits** (transactional) |
| Binary name | `asc` | `gplay` |
| Upload format | IPA | AAB (Android App Bundle) |
| Tracks | TestFlight groups | internal / alpha / beta / production |
| Review tool | TestFlight | Play Console reviews |

## Notes

- The edits model means most publishing operations need 3 steps: create edit, make changes, commit edit. The CLI should abstract this transparently.
- Rate limits: don't publish more than once per day for alpha/beta, less for production.
- Official Go client: `google.golang.org/api/androidpublisher/v3` — prefer this over raw HTTP calls.
- For auth, use `golang.org/x/oauth2/google` package.
