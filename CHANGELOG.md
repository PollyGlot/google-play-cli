# Changelog

All notable changes to `gplay` are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.11.0](https://github.com/PollyGlot/google-play-cli/compare/v0.10.0...v0.11.0) (2026-06-30)


### Features

* **edits:** explicit Edit lifecycle — `gplay edits begin/commit/discard/status` ([#48](https://github.com/PollyGlot/google-play-cli/issues/48)) ([#318](https://github.com/PollyGlot/google-play-cli/issues/318)) ([c5e86d5](https://github.com/PollyGlot/google-play-cli/commit/c5e86d5879dff0fd116f05076733042be0cb5587))

## [0.10.0](https://github.com/PollyGlot/google-play-cli/compare/v0.9.0...v0.10.0) (2026-06-26)


### Features

* **orders:** `orders view <orderId>` single-order lookup (orders.get) ([#282](https://github.com/PollyGlot/google-play-cli/issues/282)) ([#314](https://github.com/PollyGlot/google-play-cli/issues/314)) ([0a87bea](https://github.com/PollyGlot/google-play-cli/commit/0a87bea6e3246f94067fdf8792eec9359af86b6d))
* **orders:** batch `orders view` + gated `orders refund` — completes PRD [#245](https://github.com/PollyGlot/google-play-cli/issues/245) ([#283](https://github.com/PollyGlot/google-play-cli/issues/283), [#284](https://github.com/PollyGlot/google-play-cli/issues/284)) ([#317](https://github.com/PollyGlot/google-play-cli/issues/317)) ([ef2b0d5](https://github.com/PollyGlot/google-play-cli/commit/ef2b0d54ec487f51170f39ce9a92b0c48679e632))

## [0.9.0](https://github.com/PollyGlot/google-play-cli/compare/v0.8.0...v0.9.0) (2026-06-22)


### Features

* **customapps:** create managed Google Play private apps ([#285](https://github.com/PollyGlot/google-play-cli/issues/285)) ([#303](https://github.com/PollyGlot/google-play-cli/issues/303)) ([3056b75](https://github.com/PollyGlot/google-play-cli/commit/3056b75a8d47bf1ec905b1c27ae264911f98b990))
* **releases:** generated APKs — list/download the APKs Play generates from an AAB ([#299](https://github.com/PollyGlot/google-play-cli/issues/299)) ([#305](https://github.com/PollyGlot/google-play-cli/issues/305)) ([2f55eb2](https://github.com/PollyGlot/google-play-cli/commit/2f55eb20991b08665935b23935a6a41d457559a6))
* **reviews:** `reviews view <reviewId>` single-review lookup (reviews.get) ([#298](https://github.com/PollyGlot/google-play-cli/issues/298)) ([#304](https://github.com/PollyGlot/google-play-cli/issues/304)) ([e3e30bd](https://github.com/PollyGlot/google-play-cli/commit/e3e30bd0b413080a3db2784e5d5397c6bdf217c8))

## [0.8.0](https://github.com/PollyGlot/google-play-cli/compare/v0.7.0...v0.8.0) (2026-06-18)


### Features

* **install-skills:** ship gplay install-skills to surface companion skills ([#266](https://github.com/PollyGlot/google-play-cli/issues/266)) ([#273](https://github.com/PollyGlot/google-play-cli/issues/273)) ([e76d3c9](https://github.com/PollyGlot/google-play-cli/commit/e76d3c9ac9366cb63a85ee644faff3526af69dbb))
* PRD [#243](https://github.com/PollyGlot/google-play-cli/issues/243) — Android Publisher long tail (sharing, recovery, device-tiers, expansion-files) ([#280](https://github.com/PollyGlot/google-play-cli/issues/280)) ([36d1916](https://github.com/PollyGlot/google-play-cli/commit/36d19162c0071176923c247e02f6544d08859ce2))
* **site:** agent-discovery surfaces — Markdown negotiation, Link headers, skills index ([#268](https://github.com/PollyGlot/google-play-cli/issues/268)) ([f33d5ff](https://github.com/PollyGlot/google-play-cli/commit/f33d5ff47e69f743f1df6b0dc12cb97fe1aaecd3))
* **team:** PRD [#271](https://github.com/PollyGlot/google-play-cli/issues/271) — team users view &lt;email&gt; ([#274](https://github.com/PollyGlot/google-play-cli/issues/274)) ([8ff37e9](https://github.com/PollyGlot/google-play-cli/commit/8ff37e95693e3989e5a80435943d4b9cab1055f6))

## [0.7.0](https://github.com/PollyGlot/google-play-cli/compare/v0.6.0...v0.7.0) (2026-06-16)


### Features

* ✓ success-confirmation on all payload-bearing Play-API mutations (DESIGN §8) ([#256](https://github.com/PollyGlot/google-play-cli/issues/256)) ([ec9c4a0](https://github.com/PollyGlot/google-play-cli/commit/ec9c4a0d2ac664024b078bc4dfcc8a98770d05b9))
* **releases:** upload ProGuard/R8 mappings — edits.deobfuscationfiles (PRD [#250](https://github.com/PollyGlot/google-play-cli/issues/250)) ([#264](https://github.com/PollyGlot/google-play-cli/issues/264)) ([4d84dc2](https://github.com/PollyGlot/google-play-cli/commit/4d84dc270d89a9c43ace1a4985f00bf3cf2295ec))
* **vitals:** Play Developer Reporting — crashes/ANR, errors, anomalies (PRD [#49](https://github.com/PollyGlot/google-play-cli/issues/49)) ([#263](https://github.com/PollyGlot/google-play-cli/issues/263)) ([cb8cc2e](https://github.com/PollyGlot/google-play-cli/commit/cb8cc2edba6e35ddfc9e997c0c8378558a03fb02))


### Bug Fixes

* **releases:** align versionCode value with other table keys ([#239](https://github.com/PollyGlot/google-play-cli/issues/239)) ([f8c31a5](https://github.com/PollyGlot/google-play-cli/commit/f8c31a58d4866691109668dcf93f7ba8a96a17a3))
* **releases:** reject non-regular AAB paths as exit 20, not transport errors ([#265](https://github.com/PollyGlot/google-play-cli/issues/265)) ([68e6cfa](https://github.com/PollyGlot/google-play-cli/commit/68e6cfa5a74b7e38a9e7e530c80fba07fb5a2119))

## [0.6.0](https://github.com/PollyGlot/google-play-cli/compare/v0.5.0...v0.6.0) (2026-06-11)


### Features

* **platform:** operational hardening — verified install, bounded runtime, environment-enforced authority ([#206](https://github.com/PollyGlot/google-play-cli/issues/206)) ([#219](https://github.com/PollyGlot/google-play-cli/issues/219)) ([8dc8b66](https://github.com/PollyGlot/google-play-cli/commit/8dc8b66b9b5c9730d411fc6ea28b2ffb88e739d1))


### Bug Fixes

* **ci:** grant attestations:write to release-please's reusable-workflow caller ([#225](https://github.com/PollyGlot/google-play-cli/issues/225)) ([6cd0fd1](https://github.com/PollyGlot/google-play-cli/commit/6cd0fd1bdceaf75c6abf69ff876a9a70618763df)), closes [#219](https://github.com/PollyGlot/google-play-cli/issues/219)

## [0.5.0](https://github.com/PollyGlot/google-play-cli/compare/v0.4.1...v0.5.0) (2026-06-08)


### Features

* **discovery:** offline androidpublisher v3 snapshot + tooling ([#52](https://github.com/PollyGlot/google-play-cli/issues/52)) ([#197](https://github.com/PollyGlot/google-play-cli/issues/197)) ([0819019](https://github.com/PollyGlot/google-play-cli/commit/0819019f74a1bfb387ced07fcde3268776f452d6))
* **schema:** gplay schema — embedded offline API introspection ([#199](https://github.com/PollyGlot/google-play-cli/issues/199), [#200](https://github.com/PollyGlot/google-play-cli/issues/200), [#201](https://github.com/PollyGlot/google-play-cli/issues/201)) ([#203](https://github.com/PollyGlot/google-play-cli/issues/203)) ([a4b3920](https://github.com/PollyGlot/google-play-cli/commit/a4b39209f5daca058d54bd46b2477e9bdc2b0ba0))

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
