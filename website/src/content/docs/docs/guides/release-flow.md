---
title: Release flow
description: "The complete gplay release lifecycle: upload an AAB to internal, promote through beta, stage a production rollout, ramp, halt, or complete it."
sidebar:
  order: 1
---

This is the canonical path an Android release takes through gplay, from CI
build to 100% of production users. All commands assume a
[pinned package](/docs/concepts/configuration/); add
`--package com.example.myapp` otherwise.

## 1. Upload to internal

```sh
gplay releases upload app/build/outputs/bundle/release/app-release.aab \
  --track internal \
  --release-notes-dir ./whatsnew
```

The versionCode is read from the AAB itself. On a testing track the release
lands as `completed` at 100% of that track's audience. Behind the scenes
this is one [implicit Edit](/docs/concepts/edits/):
`edits.insert → bundles.upload → tracks.update → edits.commit`, auto-discarded
if anything fails.

## 2. Promote a green build

```sh
gplay releases promote --from internal --to beta
```

Promotion copies the **latest release** on the source track to the target:
same versionCode, no re-upload. Add `--version-code N` (or
`--release-name <name>`) when the source track holds more than one release;
if it's ambiguous and you pass nothing, gplay refuses with exit `60` rather
than guess.

## 3. Enter production, as a draft

```sh
gplay releases promote --from beta --to production
```

By default this creates a **draft** release on production: visible in the
Play Console, shipped to nobody. This is
[deliberate](/docs/concepts/tracks-and-releases/): reaching real users is
always an explicit step.

## 4. Stage, ramp, complete

```sh
# Start at 5% of users (requires --confirm on production).
gplay releases rollout --track production --to 0.05 --confirm

# Ramp up over the following days.
gplay releases rollout --track production --to 0.20 --confirm
gplay releases rollout --track production --to 0.50 --confirm

# Finish: userFraction → 1.0, status → completed.
gplay releases complete --track production --confirm
```

## If metrics go bad

```sh
gplay releases halt --track production                # pause the staged rollout
gplay releases resume --track production --confirm    # continue after the fix
```

A halted release stays on the track; `resume` picks it up where it stopped.
Halting never needs `--confirm`, because reducing exposure is always safe.
Resuming reaches real users again, so it does.

## Inspecting state

```sh
gplay releases list --track production
gplay tracks view --track production
```

Both support `--output json` (the raw API shape) for scripting; see
[output formats](/docs/concepts/output-formats/).

## Rehearsing a write

Every write verb accepts `--dry-run`: inputs are validated and the exact
payload is printed, with **no HTTP call**. Use it in CI to lint a release
step before the real run.

## Related

- [CI/CD guide](/docs/guides/ci-cd/): this flow in GitHub Actions
- [`gplay releases` reference](/docs/reference/releases/)
