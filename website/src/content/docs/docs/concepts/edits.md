---
title: The Edits model
description: How gplay wraps the Google Play Developer API's transactional Edits — implicit one-shot transactions today, explicit batching planned.
sidebar:
  order: 3
---

Most write operations on the Google Play Developer API are **transactional**:
you open an *Edit* on a package, accumulate changes (releases, listings,
tracks, testers), and commit it atomically. Edits expire after roughly 24
hours and are exclusive per app — only one can be open at a time.

gplay keeps Google's term **Edit** (rather than "transaction" or
"changeset") so what you read here matches the official API docs.

## Implicit Edits — the default

Every gplay write command wraps the full lifecycle in one invocation:

```txt
edits.insert  →  change (upload/patch)  →  edits.commit
```

For example, `gplay releases upload` performs
`edits.insert → bundles.upload → tracks.update → edits.commit` in a single
call. You never see the Edit ID unless you ask for it with `--verbose`.

**On any failure after the Edit opens, gplay auto-discards it** before the
error propagates — no half-open transactions left to block the next run.
Pass `--keep-edit-on-failure` to skip that cleanup when debugging.

## Dry runs

Write commands accept `--dry-run`: validate inputs and preview the exact
payload that would be sent, **without any HTTP call**. Combined with
`--output json`, this is the safest way for scripts and agents to check a
mutation before performing it.

## Explicit Edits — planned

An explicit mode — `gplay edits begin / commit / discard`, batching several
changes into one transaction with the open Edit ID persisted to
`.gplay/edit-<package>.json` — is designed but **not shipped yet**. It is
tracked in
[issue #48](https://github.com/PollyGlot/google-play-cli/issues/48). Today,
every write is its own implicit transaction.

## Related

- [Tracks & releases](/docs/concepts/tracks-and-releases/)
- [`gplay releases` reference](/docs/reference/releases/)
