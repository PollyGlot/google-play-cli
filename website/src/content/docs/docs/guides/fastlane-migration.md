---
title: Migrating from Fastlane
description: Map fastlane supply to gplay commands — drop the Ruby runtime from your Android CI, keep your metadata tree, and learn the one behavioral difference on production uploads.
sidebar:
  order: 8
---

gplay covers the `fastlane supply` surface (and more) as a single static
binary — no Ruby in the CI image. Migration is mostly mechanical.

## The one behavioral surprise

Coming from Fastlane, the single most common surprise:
`gplay releases upload --track production` does **not** publish to 100% by
default. It creates a `draft` release that a follow-up step must explicitly
publish with `--complete` or `--staged <fraction>` (plus `--confirm`). This
is [deliberate](/docs/concepts/tracks-and-releases/). On every other track
the behavior matches Fastlane: `completed` at 100%.

## Command mapping

| fastlane | gplay |
| --- | --- |
| `supply --aab app.aab --track internal` | `gplay releases upload app.aab --track internal` |
| `supply --track internal --track_promote_to beta` | `gplay releases promote --from internal --to beta` |
| `supply --rollout 0.1` | `gplay releases rollout --track production --to 0.1 --confirm` |
| `supply --skip_upload_apk ... metadata sync` | `gplay metadata apply` / `gplay metadata images apply` |
| `supply --metadata_path ./metadata` | `gplay metadata apply --dir ./metadata` |

## Your metadata tree drops in

The on-disk layout is fastlane's, minus the redundant `android/` segment and
minus `changelogs/`:

- `title.txt`, `short_description.txt`, `full_description.txt`, `video.txt`
  per locale — identical names.
- Release notes move out of the metadata tree: gplay owns them at upload
  time via `--release-notes` or `--release-notes-dir ./whatsnew` (one
  `<locale>.txt` per file, optional `default.txt` fallback).

Start with `gplay metadata pull` into a fresh directory and diff it against
your fastlane tree — differences are usually un-pushed edits.

## Authentication is the same JSON

The Google Cloud service-account JSON you already use with Fastlane works
as-is. In CI, export it inline as `GPLAY_SERVICE_ACCOUNT`; locally,
`gplay auth login --service-account ./sa.json` stores it in the OS keychain.
`gplay auth doctor` verifies the wiring with a real API round-trip.

## What you gain

- No Ruby runtime or `bundle install` in CI — one binary, fast cold start.
- [Semantic exit codes](/docs/concepts/exit-codes/) — retry on `40`/`50`,
  fail fast on everything else, no log parsing.
- JSON output that mirrors the
  [Play API responses](/docs/concepts/output-formats/) for scripting.
- `--dry-run` on every write.

## Honest gaps

gplay is pre-1.0; a detailed pitfall-by-pitfall migration table is planned
once more real migrations surface them. If you hit one, please
[open an issue](https://github.com/PollyGlot/google-play-cli/issues).

## Related

- [Quickstart](/docs/getting-started/quickstart/)
- [CI/CD integration](/docs/guides/ci-cd/)
