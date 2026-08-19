---
title: Closed tracks & testers
description: "Create closed testing tracks and declare their authorized Google Groups with gplay: declarative tester management for QA teams and external betas."
sidebar:
  order: 5
---

A **closed track** gates a build to an explicit audience: a QA team, an
external beta cohort. gplay creates the track and declares who can test it.

## Create a closed track

```sh
gplay tracks create qa-team
```

Closed testing is the only track type the API can create, so there is no
`--type` flag. The new track is a phone (default form factor) track.
Creating a track that already exists surfaces the API error (exit `30`);
gplay doesn't fake idempotency.

## Declare the testers

The Play API expresses a track's audience **only as Google Groups**;
individual emails are a Play Console-only gesture:

```sh
gplay testers set --track qa-team --group qa@googlegroups.com
gplay testers list --track qa-team
```

`testers set` is **declarative**: it replaces the whole group list (it maps
1:1 to the API's `testers.update`). There is no add/remove: re-run `set`
with the full desired list. Idempotent by design, which is exactly what CI
and agents want.

Two safety rails:

- A bare `set` with neither `--group` nor `--clear` is refused (exit `2`), so
  a forgotten `--group` can never silently wipe the audience.
- Emptying the list on purpose is the explicit `--clear`.

There is no `--confirm` here: a closed test track is low-stakes and
reversible, unlike a production rollout.

## Ship a build to the track

```sh
gplay releases upload app.aab --track qa-team
gplay releases promote --from internal --to qa-team
```

`--track` accepts any name. If the track doesn't exist yet, the upload fails
with a hint pointing at `gplay tracks create`. gplay never auto-creates a
track from a typo.

## Rehearse first

Both writes accept `--dry-run` (validate + preview the payload, no HTTP
call) and `--keep-edit-on-failure` for debugging the underlying
[Edit](/docs/concepts/edits/).

## Related

- [Tracks & releases](/docs/concepts/tracks-and-releases/)
- [`gplay testers` reference](/docs/reference/testers/)
- [`gplay tracks` reference](/docs/reference/tracks/)
