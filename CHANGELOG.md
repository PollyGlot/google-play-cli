# Changelog

All notable changes to `gplay` are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0-alpha.2] — 2026-05-26

Second pre-release. Lands the core Fastlane-replacement surface
(`releases upload`, `promote`, `rollout`) plus the Apps registry
foundation (`apps add`, `list`, `info`).

This is still **not** a stability commitment — `v0.1.0` stable arrives
once `tracks` and `reviews` ship.

### Added

- `gplay releases upload <aab>` — AAB upload through the Edits model
  (`insert edit` → upload bundle → `tracks.update` → `commit edit`),
  with `--track`, `--user-fraction`, and `--dry-run` flags.
- `gplay releases promote --from <track> --to <track>` — promote an
  already-uploaded build between tracks without re-uploading.
- `gplay releases rollout` — staged rollout state machine with
  `rollout` / `halt` / `resume` / `complete` subcommands and explicit
  user-fraction control.
- `gplay releases list` — read-only view of releases per track,
  backed by `tracks.get`.
- `gplay apps add` — register an app in the local registry with
  edits-validation against the Google Play Developer API
  (`edits.Validate` round-trip).
- `gplay apps list` — list registry entries scoped to the active
  account.
- `gplay apps info` — fetch app details and per-locale store listings
  (verbatim envelope from `details.get` + `listings.get`).

### Fixed

- `gplay version` — fall back to `debug.BuildInfo` when no ldflags
  metadata is embedded, so `go install …@latest` builds report a
  meaningful version instead of `dev`.

### Not in this release

- `gplay tracks list` / `status`
- `gplay reviews list` / `reply`
- `gplay apps remove`
- `gplay vitals`, `metadata`, `subscriptions`, `iap`

## [0.1.0-alpha.1] — 2026-05-22

First public pre-release. The goal is to exercise the release pipeline
(GoReleaser, install paths) and unblock `go install …@latest` while the
functional surface is still small.

This is **not** a stability commitment. Expect breaking changes before
`v0.1.0`. The Fastlane-replacement story (`releases upload`, `tracks`,
`reviews`) lands in `v0.1.0` stable.

### Added

- `gplay auth login / logout / status / list / doctor` — full service-account
  lifecycle with explicit active-account selection.
- Auth keystore — OS-native backends (macOS Keychain, libsecret on Linux,
  Windows Credential Manager) with a `0600` file fallback when no native
  backend is available.
- Resolver precedence for the active account: explicit flag → environment
  variable (`GPLAY_SERVICE_ACCOUNT`) → keystore active account.
- `gplay auth doctor` — non-API sanity checks plus per-package round-trip
  validation against the Google Play Developer API.
- `gplay init` — cascading config foundation (project → user → defaults).
- Output dispatcher — TTY-aware `table` default, automatic `json` when piped,
  opt-in `markdown` mode (`--output markdown`).

### Not in this release

The following commands from the roadmap are **not** included yet:

- `gplay releases upload` / `promote` / `rollout`
- `gplay tracks list` / `status`
- `gplay reviews list` / `reply`
- `gplay apps add` / `list` / `info` / `remove` (Apps registry slice)
- `gplay vitals`, `metadata`, `subscriptions`, `iap`

These ship incrementally on the road to `v0.1.0` stable.

[Unreleased]: https://github.com/PollyGlot/google-play-cli/compare/v0.1.0-alpha.2...HEAD
[0.1.0-alpha.2]: https://github.com/PollyGlot/google-play-cli/compare/v0.1.0-alpha.1...v0.1.0-alpha.2
[0.1.0-alpha.1]: https://github.com/PollyGlot/google-play-cli/releases/tag/v0.1.0-alpha.1
