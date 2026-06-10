---
title: Tracks & releases
description: Standard and closed tracks, safe production defaults, the staged rollout state machine, and how gplay targets releases.
sidebar:
  order: 6
---

## Tracks

Google Play provisions four **standard tracks** for every app, in promotion
order: `internal`, `alpha`, `beta`, `production`. Beyond those, an app can
have **closed tracks** with custom names (`qa-team`, `external-beta`, …) —
the only kind gplay can create, because closed testing is the only type the
API's create endpoint supports:

```sh
gplay tracks create qa-team
```

There is no `tracks delete` — the API exposes none; removing a track is a
Play Console-only gesture.

The `--track` flag on release commands is **passthrough**: any string is
accepted, so closed tracks work everywhere the four standard names do. A
typo'd track name fails at the API — gplay never auto-creates a track as a
side effect.

## Safe production defaults

What status a new release gets depends on the target track:

| Target track | Default status | Default userFraction |
| --- | --- | --- |
| `production` | **`draft`** | — |
| `internal` / `alpha` / `beta` / closed | `completed` | `1.0` |

Shipping to real users is always explicit: `--complete` (100%) or
`--staged <fraction>`, and on production those additionally require
`--confirm`. Everything else defaults to the behavior you'd expect from a
testing track.

## The rollout state machine

Each transition on a staged production rollout is its own verb:

```sh
gplay releases rollout --track production --to 0.05   # set userFraction
gplay releases halt --track production                # pause on bad metrics
gplay releases resume --track production              # continue
gplay releases complete --track production            # → 100%, completed
```

## Targeting a release

- `releases upload <aab>` reads the versionCode **from the AAB** — never a
  flag, so it can't disagree with the artifact.
- `promote` / `rollout` / `halt` / `resume` / `complete` target the
  **latest** release on the track. Override with `--version-code N` or
  `--release-name <name>`. If two releases coexist on the track (e.g. one
  `inProgress` and one `halted`) and no selector is passed, gplay **refuses
  with exit `60`** rather than guess.

## Release notes

Two mutually exclusive flags on `releases upload`:

- `--release-notes "<text>"` — one text, applied to the app's default
  language.
- `--release-notes-dir <dir>` — one file per locale (`en-US.txt`,
  `fr-FR.txt`, …), with an optional `default.txt` fallback for locales
  without a dedicated file.

## Country availability

Per-track country availability (`syncWithProduction`, `restOfWorld`,
targeted countries) is **read-only on the API**, so gplay surfaces it as a
read: `gplay tracks availability view --track production`. Changing it is a
Play Console gesture.

## Related

- [Release flow guide](/docs/guides/release-flow/)
- [Testers guide](/docs/guides/testers/)
- [`gplay releases` reference](/docs/reference/releases/)
