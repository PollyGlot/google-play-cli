---
title: The Edits model
description: "How gplay wraps the Google Play Developer API's transactional Edits: implicit one-shot transactions by default, explicit batching with gplay edits begin/commit."
sidebar:
  order: 3
---

Most write operations on the Google Play Developer API are **transactional**:
you open an *Edit* on a package, accumulate changes (releases, listings,
tracks, testers), and commit it atomically. Edits expire after roughly 24
hours and are exclusive per app: only one can be open at a time.

gplay keeps Google's term **Edit** (rather than "transaction" or
"changeset") so what you read here matches the official API docs.

## Implicit Edits: the default

With no explicit Edit open, every gplay write command wraps the full
lifecycle in one invocation:

```txt
edits.insert  →  change (upload/patch)  →  edits.commit
```

For example, `gplay releases upload` performs
`edits.insert → bundles.upload → tracks.update → edits.commit` in a single
call. You never see the Edit ID unless you ask for it with `--verbose`.

**On any failure after the Edit opens, gplay auto-discards it** before the
error propagates, so no half-open transaction is left to block the next run.
Pass `--keep-edit-on-failure` to skip that cleanup when debugging.

## Dry runs

Write commands accept `--dry-run`: validate inputs and preview the exact
payload that would be sent, **without any HTTP call**. Combined with
`--output json`, this is the safest way for scripts and agents to check a
mutation before performing it.

## Explicit Edits: batching several changes

When you want one atomic transaction to span several commands, open the Edit
yourself:

```sh
gplay edits begin                     # opens the Edit, pins it in .gplay/
gplay metadata apply
gplay releases upload app.aab --track internal
gplay edits status                    # which Edit is pinned, if any
gplay edits commit                    # publish everything at once
```

`edits begin` persists the Edit ID to `.gplay/edit-<package>.json`
(gitignored). While that pin exists, write commands reuse the open Edit
instead of opening their own, and they no longer commit on their own: the
lifecycle is yours until `gplay edits commit` publishes or
`gplay edits discard` abandons it. There is no auto-commit and no
auto-discard in explicit mode.

Two guard rails, both exit `60`: opening a second Edit while one is pinned is
refused, and committing or discarding with nothing pinned is refused. A
project is required, since the pin lives in `.gplay/` (run `gplay init`
first).

## Related

- [Tracks & releases](/docs/concepts/tracks-and-releases/)
- [`gplay edits` reference](/docs/reference/edits/)
- [`gplay releases` reference](/docs/reference/releases/)
