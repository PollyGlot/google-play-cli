# Changelog

All notable changes to `gplay` are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.4.1](https://github.com/PollyGlot/google-play-cli/compare/v0.4.0...v0.4.1) (2026-06-04)


### Bug Fixes

* **auth:** defer keystore probe until a credential is actually needed ([#172](https://github.com/PollyGlot/google-play-cli/issues/172)) ([ca77148](https://github.com/PollyGlot/google-play-cli/commit/ca77148f892e4228112e5276df2ff72160568a32))
* **auth:** surface invalid-credential errors from EnsureAccount (absent vs invalid) ([#176](https://github.com/PollyGlot/google-play-cli/issues/176)) ([#182](https://github.com/PollyGlot/google-play-cli/issues/182)) ([ac4f9d7](https://github.com/PollyGlot/google-play-cli/commit/ac4f9d77d660a1d76bc150a95443eefa1a94f2b5))
* **cli:** map invalid-flag errors to exit 2 (CLI misuse, §9) ([#175](https://github.com/PollyGlot/google-play-cli/issues/175)) ([9c9b3de](https://github.com/PollyGlot/google-play-cli/commit/9c9b3de4d5ed11ff60d5c245c0d868ca02869566))
* **cli:** reject unknown subcommands on every group and the root (exit 2) ([#173](https://github.com/PollyGlot/google-play-cli/issues/173)) ([82c1711](https://github.com/PollyGlot/google-play-cli/commit/82c17110b22c44cc0f0731eefdec96c6621a9481))

## [0.4.0](https://github.com/PollyGlot/google-play-cli/compare/v0.3.0...v0.4.0) (2026-06-02)


### Features

* **team:** team management — users & grants (developer-account permissions) ([#147](https://github.com/PollyGlot/google-play-cli/issues/147)) ([#159](https://github.com/PollyGlot/google-play-cli/issues/159)) ([617268f](https://github.com/PollyGlot/google-play-cli/commit/617268f99245f7538d2199ed3147f0008010ae40))

## [0.3.0](https://github.com/PollyGlot/google-play-cli/compare/v0.2.0...v0.3.0) (2026-06-01)


### Features

* **apps,tracks:** App details (read+set) + Country availability (read-only) — PRD [#113](https://github.com/PollyGlot/google-play-cli/issues/113) ([#138](https://github.com/PollyGlot/google-play-cli/issues/138)) ([f6a172a](https://github.com/PollyGlot/google-play-cli/commit/f6a172a0bcb46956ca25965b6102f63163c07805))
* **compliance:** Data Safety declaration — write-only datasafety surface (PRD [#114](https://github.com/PollyGlot/google-play-cli/issues/114)) ([#140](https://github.com/PollyGlot/google-play-cli/issues/140), [#141](https://github.com/PollyGlot/google-play-cli/issues/141), [#142](https://github.com/PollyGlot/google-play-cli/issues/142)) ([#144](https://github.com/PollyGlot/google-play-cli/issues/144)) ([1c275d0](https://github.com/PollyGlot/google-play-cli/commit/1c275d0ec72f04c2d6be28a99badd498b49f339e))
* **metadata:** images family — list/pull/validate/apply (+prune) (PRD [#112](https://github.com/PollyGlot/google-play-cli/issues/112)) ([#139](https://github.com/PollyGlot/google-play-cli/issues/139)) ([366fa47](https://github.com/PollyGlot/google-play-cli/commit/366fa47ead89ba7b1faebb7ce1001475d7c6454c))
* **metadata:** listings family — list/pull/validate/apply (PRD [#50](https://github.com/PollyGlot/google-play-cli/issues/50)) ([#110](https://github.com/PollyGlot/google-play-cli/issues/110)) ([ee00165](https://github.com/PollyGlot/google-play-cli/commit/ee001659ae10e229fe861a74f072b4fbe3453ce4))
* **tracks:** closed tracks + testers — create/list/set + create hint (PRD [#117](https://github.com/PollyGlot/google-play-cli/issues/117)) ([#125](https://github.com/PollyGlot/google-play-cli/issues/125)) ([68fb3d1](https://github.com/PollyGlot/google-play-cli/commit/68fb3d135154d0e106acb5fd53bc6146414c6869))

## [0.2.0](https://github.com/PollyGlot/google-play-cli/compare/v0.1.0...v0.2.0) (2026-05-29)


### Features

* **install:** serve install script via gplay.sh Cloudflare Worker ([#87](https://github.com/PollyGlot/google-play-cli/issues/87)) ([f4ee120](https://github.com/PollyGlot/google-play-cli/commit/f4ee120a1c14d4e67df1a920a468db2c32e969d9))
* **reviews:** gplay reviews list (7-day window, auto-paginated, --stars filter) ([#61](https://github.com/PollyGlot/google-play-cli/issues/61)) ([#92](https://github.com/PollyGlot/google-play-cli/issues/92)) ([344879e](https://github.com/PollyGlot/google-play-cli/commit/344879edf5df8ad9e65b6e8faf38ec6147090fbf))
* **reviews:** gplay reviews reply — single + --batch ([#62](https://github.com/PollyGlot/google-play-cli/issues/62)) ([#96](https://github.com/PollyGlot/google-play-cli/issues/96)) ([dd0aab5](https://github.com/PollyGlot/google-play-cli/commit/dd0aab52af9b3bee071afa566255d1b0589c9dae))


### Bug Fixes

* **install:** print version via `gplay version`, not `--version` ([#88](https://github.com/PollyGlot/google-play-cli/issues/88)) ([37794b1](https://github.com/PollyGlot/google-play-cli/commit/37794b1bf566f43731ccc963532966550c24a7db))
* **worker:** source account_id from a CI variable so the minimal token can deploy ([#91](https://github.com/PollyGlot/google-play-cli/issues/91)) ([ff93fe2](https://github.com/PollyGlot/google-play-cli/commit/ff93fe26d261641c279b38569027328a20077cd9))

## [0.1.0](https://github.com/PollyGlot/google-play-cli/compare/v0.1.0-alpha.2...v0.1.0) (2026-05-29)


### Features

* **tracks:** gplay tracks list (cross-track view) ([#77](https://github.com/PollyGlot/google-play-cli/issues/77)) ([eafaa2c](https://github.com/PollyGlot/google-play-cli/commit/eafaa2ce1aec94aec7afaee20040fc494aa2199f))
* **tracks:** gplay tracks status (single-track deep view) ([#78](https://github.com/PollyGlot/google-play-cli/issues/78)) ([38e73c1](https://github.com/PollyGlot/google-play-cli/commit/38e73c1a94daaa34c0673d6a5e4d379e0a82454b))


### Bug Fixes

* **tracks:** tracks status JSON guard + un-mask markdown header test ([#79](https://github.com/PollyGlot/google-play-cli/issues/79)) ([3696643](https://github.com/PollyGlot/google-play-cli/commit/3696643795c4074379a61f30592ad7879b5237f0))


### Miscellaneous Chores

* graduate release to 0.1.0 ([#84](https://github.com/PollyGlot/google-play-cli/issues/84)) ([46399bc](https://github.com/PollyGlot/google-play-cli/commit/46399bc50bee85d7028eedf744c0995adf18d34b))

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
- `gplay apps remove` — drop an entry from the local registry. Purely
  local cleanup, no HTTP round-trip.

### Fixed

- `gplay version` — fall back to `debug.BuildInfo` when no ldflags
  metadata is embedded, so `go install …@latest` builds report a
  meaningful version instead of `dev`.

### Not in this release

- `gplay tracks list` / `status`
- `gplay reviews list` / `reply`
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

[0.1.0-alpha.2]: https://github.com/PollyGlot/google-play-cli/compare/v0.1.0-alpha.1...v0.1.0-alpha.2
[0.1.0-alpha.1]: https://github.com/PollyGlot/google-play-cli/releases/tag/v0.1.0-alpha.1
