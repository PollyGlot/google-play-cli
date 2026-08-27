<p align="center">
  <img src="docs/marketing/header.png" alt="gplay: standalone Go binary for the Google Play Developer API, built for CI, scripts, and AI agents" width="720" />
</p>

<p align="center">
  <a href="https://gplay.sh"><img src="https://img.shields.io/badge/website-gplay.sh-2ea44f?style=for-the-badge" alt="Website: gplay.sh"></a>
  <a href="https://github.com/PollyGlot/google-play-cli/releases"><img src="https://img.shields.io/github/v/release/PollyGlot/google-play-cli?include_prereleases&style=for-the-badge&color=blue&label=release" alt="Latest Release"></a>
  <a href="https://github.com/PollyGlot/google-play-cli/stargazers"><img src="https://img.shields.io/github/stars/PollyGlot/google-play-cli?style=for-the-badge" alt="GitHub Stars"></a>
  <img src="https://img.shields.io/badge/Go-1.22+-00ADD8?style=for-the-badge&logo=go" alt="Go Version">
  <img src="https://img.shields.io/badge/License-MIT-yellow?style=for-the-badge" alt="License">
  <img src="https://img.shields.io/badge/status-GA-2ea44f?style=for-the-badge" alt="Status">
</p>

# gplay

Publish to Google Play from a CI pipeline or a coding agent (Claude Code,
Codex, Cursor), without Fastlane. Shipping `supply` means shipping a Ruby
runtime in your image, output meant for humans, and generic exit codes that
turn retry logic into guesswork. `gplay` replaces it with one static binary,
nothing to install alongside it, and exit codes you can branch on.

Two properties do the heavy lifting for a non-human caller. `--output json`
returns the Google Play Developer API response verbatim: an agent that knows
Google's API already knows gplay's output, field for field, including the
zero values a typed re-marshal would drop. And every command declares its
contract: unmarked means frozen under the Public contract, `[experimental]`
means free to evolve. `--help` tells you which, per command.

**Two ways to drive it:** the raw CLI (flags, scripts, CI) or
[agent skills](#agent-skills) that run it from natural-language prompts.

> **Generally available.** The Public contract is in force: for every command
> *not* marked `[experimental]`, names, flags, semantics and exit codes will not
> change without a major bump. That covers auth, apps, the core release loop,
> tracks, testers, team, edits, metadata, compliance, vitals, and
> `reviews list` / `view` / `reply`. Newer or less-exercised commands ship
> `[experimental]` and stay free to evolve: monetization, `games`, `recovery`,
> `orders`, `reviews history`, and the side namespaces under `releases`.
> **`--help` is the source of truth**: the label is on the command itself.
> See [Stability and the Public contract](https://gplay.sh/docs/concepts/stability/),
> [Migrating to 1.0](https://gplay.sh/docs/guides/migrate-to-1-0/), and
> [docs/BACKLOG.md](docs/BACKLOG.md) for what's out of scope.

## Why

- **Standalone Go binary.** No Ruby, no Node, no Python: one file in your CI
  image.
- **Built for CI and agents.** TTY-aware output (`table` by default, `json`
  in pipes), explicit flags, semantic exit codes for retry decisions.
- **API-faithful.** `--output json` returns the raw Google Play Developer
  API response shape, with no custom envelope to learn. The Google docs *are*
  the schema docs.
- **Safe by default on production.** Uploading or promoting to the
  `production` track creates a `draft` release unless you explicitly
  `--complete` or `--staged <fraction>`.
  See [ADR-0002](docs/adr/0002-safe-production-defaults.md).

## Install

Pick `go install`, Homebrew, or the install script. Pre-built binaries for
Linux, macOS, and Windows are on the
[releases page](https://github.com/PollyGlot/google-play-cli/releases).

```bash
# go install
go install github.com/PollyGlot/google-play-cli/cmd/gplay@latest

# Homebrew
brew install PollyGlot/tap/gplay

# Install script
curl -fsSL https://gplay.sh/install | sh
```

The install script verifies the archive's SHA-256 against the release
`checksums.txt` and **fails closed**: a missing, incomplete, or mismatched
checksum aborts the install. Set `GPLAY_INSTALL_NO_VERIFY=1` to bypass (prints
a warning, greppable in CI). To add cosign and provenance checks on top, see
[Verify a release](#verify-a-release).

## Agent skills

`gplay` is shaped for a non-human caller: explicit flags, no interactive
prompts, machine-readable output. A skill turns a natural-language prompt into
the matching invocation, safe defaults included:

> *"Promote the latest internal build of com.example.myapp to beta."*
> → the `gplay-release-flow` skill runs `gplay releases promote --from
> internal --to beta` for you.

Install them in one step. It needs `git` on your PATH, and nothing else:

```bash
gplay install-skills
```

The skills are fetched with `git` from a commit pinned inside the binary, never
from a branch, so two runs of the same gplay version install the same reviewed
files. Every installed file is verified against that checkout, unrelated skills
in the target directory are left alone, and a failed install is rolled back to
its previous state. They land in `~/.claude/skills` by default (`--dir` moves
them elsewhere). A new pack ships in a normal gplay release.

Skills live in a companion repo,
[**PollyGlot/google-play-cli-skills**](https://github.com/PollyGlot/google-play-cli-skills).
Each is a folder with a `SKILL.md` documenting its intent, the commands it
runs, and the limits it enforces.
[ADR-0021](docs/adr/0021-companion-skills-repo.md) fixes the roster: one skill
per shipped namespace, plus a `gplay-cli-usage` foundation.

| Skill | Drives |
|---|---|
| `gplay-cli-usage` | Cross-cutting conventions (foundation) |
| `gplay-setup` | Auth onboarding |
| `gplay-apps` | Apps registry + details |
| `gplay-release-flow` | upload / promote / rollout |
| `gplay-tracks` | Tracks + testers |
| `gplay-reviews` | reviews list / reply |
| `gplay-metadata-sync` | Listings + images |
| `gplay-compliance` | Data Safety |
| `gplay-team` | users / grants / permissions |
| `gplay-vitals` | Crash / ANR rates, error reports, anomalies (read-only) |
| `gplay-monetization` | Subscriptions and one-time products as declarative files |
| `gplay-orders` | Order lookup and refunds |
| `gplay-recovery` | App recovery actions for a bad release |
| `gplay-games` | Play Games Services achievements and leaderboards |
| `gplay-device-tiers` | Device tier configs for tiered delivery |
| `gplay-customapps` | Managed Google Play private apps |
| `gplay-appstore` | Alternative app store catalog and update feed |

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

## Replacing Fastlane

The commands the skills above drive. All shipped, all in the frozen contract:

```bash
# Upload an AAB to the internal track, with localized release notes.
gplay releases upload app.aab \
  --package com.example.myapp \
  --track internal \
  --release-notes-dir ./whatsnew

# Promote the latest internal build to beta.
gplay releases promote --package com.example.myapp --from internal --to beta

# Stage a production rollout, then advance it.
gplay releases rollout --package com.example.myapp --track production --to 0.10

# Read the most recent reviews (API exposes the last 7 days only) and reply.
gplay reviews list --package com.example.myapp --stars 1-2
gplay reviews reply --review-id REVIEW_ID --reply "Thanks for the feedback!"
```

## How it's set up

Every decision lands in a document before it lands in code, so contributors
and agents start from the same page.

- [**CLAUDE.md**](CLAUDE.md): project context and agent working
  instructions (read order, conventions, build/test, PR gate).
- [**CONTEXT.md**](CONTEXT.md): glossary of canonical terms (Edit,
  Account, Project, ...). Use them verbatim.
- [**docs/DESIGN.md**](docs/DESIGN.md): CLI conventions across commands
  (auth precedence, exit codes, output format, verbosity, edit lifecycle).
- [**docs/BACKLOG.md**](docs/BACKLOG.md): explicitly out-of-scope features.
- [**docs/CI_CD.md**](docs/CI_CD.md): how to wire `gplay` into a CI
  pipeline (GitHub Actions example).
- [**docs/adr/**](docs/adr/): Architecture Decision Records.

## Verify a release

<details>
<summary>Confirm an artifact came from this repo's release pipeline (cosign + provenance).</summary>

Every release publishes a cosign signature over `checksums.txt` and a GitHub
build-provenance attestation over each archive. Two independent checks, and
either one confirms the archive came from this repo's release pipeline before
you trust it in CI.

```bash
# 1. Build-provenance attestation (needs the GitHub CLI; no extra download).
#    Proves the archive was built by this repo's release workflow.
gh attestation verify gplay_<version>_<os>_<arch>.tar.gz \
  -R PollyGlot/google-play-cli

# 2. cosign signature over checksums.txt (needs cosign). The checksum file
#    transitively covers every archive it lists, so verify it, then check
#    your archive against it.
cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github.com/PollyGlot/google-play-cli/\.github/workflows/release\.yml@' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
shasum -a 256 -c <(grep " gplay_<version>_<os>_<arch>.tar.gz$" checksums.txt)
```

Download `checksums.txt` and `checksums.txt.sigstore.json` from the same
[release](https://github.com/PollyGlot/google-play-cli/releases) as the
archive. The install script already checks the SHA-256 against `checksums.txt`
and [fails closed](#install); these commands add provenance and signature
verification on top.

</details>

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). TL;DR: open an issue first for
anything bigger than a typo, branch from `main`, open a PR.

## License

[MIT](LICENSE). © 2026 Pavlo Trinko.

## Not affiliated with Google

`gplay` is an independent open-source project. It calls the public Google
Play Developer API. Google LLC does not endorse, sponsor, or maintain it. "Google Play" is a trademark of Google LLC.
