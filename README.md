<p align="center">
  <img src="docs/marketing/header.png" alt="gplay — standalone Go binary for the Google Play Developer API, built for CI, scripts, and AI agents" width="720" />
</p>

<p align="center">
  <a href="https://github.com/PollyGlot/google-play-cli/releases/latest"><img src="https://img.shields.io/github/v/release/PollyGlot/google-play-cli?style=for-the-badge&color=blue" alt="Latest Release"></a>
  <a href="https://github.com/PollyGlot/google-play-cli/stargazers"><img src="https://img.shields.io/github/stars/PollyGlot/google-play-cli?style=for-the-badge" alt="GitHub Stars"></a>
  <img src="https://img.shields.io/badge/Go-1.22+-00ADD8?style=for-the-badge&logo=go" alt="Go Version">
  <img src="https://img.shields.io/badge/License-MIT-yellow?style=for-the-badge" alt="License">
  <img src="https://img.shields.io/badge/status-pre--1.0-orange?style=for-the-badge" alt="Status">
</p>

# gplay

Every team shipping an Android app eventually hits the same wall: Fastlane
`supply` drags a Ruby runtime into your CI image, output meant for humans,
and generic exit codes that make retry logic guesswork. `gplay` is what
you'd build today if you started fresh — one static binary, no runtime,
JSON output that matches the Google Play Developer API verbatim, semantic
exit codes, safe production defaults.

> **Status: pre-1.0.** v0.1.0-alpha.2 is out and usable. The MVP surface
> (auth, releases, apps) ships in alpha. Tracks and reviews land next.
> Breaking changes are expected before `v0.1.0` stable — see
> [docs/BACKLOG.md](docs/BACKLOG.md) for what's intentionally out of scope.

## Why

- **Standalone Go binary.** No Ruby, no Node, no Python — one file in your CI
  image.
- **Built for CI and agents.** TTY-aware output (`table` by default, `json`
  in pipes), explicit flags, semantic exit codes for retry decisions.
- **API-faithful.** `--output json` returns the raw Google Play Developer
  API response shape — no custom envelope to learn. The Google docs *are* the
  schema docs.
- **Safe by default on production.** Uploading or promoting to the
  `production` track creates a `draft` release unless you explicitly
  `--complete` or `--staged <fraction>`.
  See [ADR-0002](docs/adr/0002-safe-production-defaults.md).

## Install

`go install` works today against tagged alpha releases. Homebrew,
install script, and pre-built binaries land with `v0.1.0` stable.

```bash
# go install — works today (alpha builds tagged)
go install github.com/PollyGlot/google-play-cli/cmd/gplay@latest

# Homebrew (planned for v0.1.0)
brew install PollyGlot/gplay/gplay

# Install script (planned for v0.1.0)
curl -fsSL https://gplay.sh/install | sh
```

## Quick start

```bash
# Point gplay at a Google Cloud service account JSON.
gplay auth login --service-account ./service_account.json

# List configured accounts and see which one is active.
gplay auth list
gplay auth status

# Verify the SA actually has access to your app.
gplay auth doctor --package com.example.myapp

# Bootstrap a project-local config (cascading: project → user → defaults).
gplay init
```

Full command reference: `gplay --help` (or `gplay <subcommand> --help`).

### Planned for v0.1 stable

The Fastlane-replacement surface lands incrementally on the road to
`v0.1.0`. These commands are not yet implemented in the current pre-release:

```bash
# Upload an AAB to the internal track.
gplay releases upload app.aab \
  --package com.example.myapp \
  --track internal \
  --release-notes-dir ./whatsnew

# Promote the latest internal build to beta.
gplay releases promote --package com.example.myapp --from internal --to beta

# Read the most recent reviews (API exposes the last 7 days only).
gplay reviews list --package com.example.myapp --stars 1-2
```

See [docs/BACKLOG.md](docs/BACKLOG.md) for the full roadmap and what is
intentionally out of scope.

## How it's set up

The repo is documentation-first by design. Before writing significant code,
the decisions are pinned in place so contributors and agents converge:

- [**CLAUDE.md**](CLAUDE.md) — project context and roadmap.
- [**AGENTS.md**](AGENTS.md) — instructions for AI agents working in this
  repo. Reads CLAUDE.md, CONTEXT.md, DESIGN.md before generating code.
- [**CONTEXT.md**](CONTEXT.md) — glossary of canonical terms (Edit,
  Account, Project, ...). Use them verbatim.
- [**docs/DESIGN.md**](docs/DESIGN.md) — CLI conventions across commands
  (auth precedence, exit codes, output format, verbosity, edit lifecycle).
- [**docs/BACKLOG.md**](docs/BACKLOG.md) — explicitly out-of-scope features.
- [**docs/CI_CD.md**](docs/CI_CD.md) — how to wire `gplay` into a CI
  pipeline (GitHub Actions example).
- [**docs/adr/**](docs/adr/) — Architecture Decision Records.

## Skills repo (planned)

Agent skills that drive `gplay` from natural-language prompts will live in a
companion repo: `PollyGlot/google-play-cli-skills`. Each skill is a folder
with a `SKILL.md` file that documents the intent, the gplay commands it
runs, and the safety rails it enforces.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). TL;DR: open an issue first for
anything bigger than a typo, branch from `main`, open a PR.

## License

[MIT](LICENSE) — © 2026 Pavlo Trinko and contributors.

## Not affiliated with Google

`gplay` is an independent open-source project. It uses the public Google
Play Developer API and is not endorsed by, affiliated with, or sponsored by
Google LLC. "Google Play" is a trademark of Google LLC.
