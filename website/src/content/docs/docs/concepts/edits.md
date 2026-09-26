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
gplay edits status --live             # ...and whether the server still has it
gplay edits validate                  # Google's commit checks, nothing published
gplay edits commit                    # publish everything at once
```

`edits begin` persists the Edit ID to `.gplay/edit-<package>.json`
(gitignored). While that pin exists, write commands reuse the open Edit
instead of opening their own, and they no longer commit on their own: the
lifecycle is yours until `gplay edits commit` publishes or
`gplay edits discard` abandons it. There is no auto-commit and no
auto-discard in explicit mode.

Before committing, `gplay edits validate` asks Google to run its commit-time
checks on the pinned Edit without publishing: exit `0` means the commit would
go through, a rejection carries the API's error with the usual exit code, and
the Edit stays open either way. Edits expire after about 24 hours, so
`gplay edits status --live` also asks the server whether the pinned Edit
still exists and reports its expiry; if it is gone, the report says so and
points at `gplay edits discard` to clear the stale pin.

Two guard rails, both exit `60`: opening a second Edit while one is pinned is
refused, and committing or discarding with nothing pinned is refused. A
project is required, since the pin lives in `.gplay/` (run `gplay init`
first).

Read commands (`releases list`, `tracks list`, `metadata pull`, ...) keep
showing the **live** state while an Edit is pinned: what is published, not
what is staged. `releases artifacts list` is the one exception and also shows
artifacts uploaded into the pinned Edit.

## Committing while changes are in review

If some changes are already in Google's review when an Edit is committed,
Google's default is to **cancel that review and submit everything again**,
which restarts the review. gplay keeps that default: with no flag, it sends
the commit exactly as before. Every command that commits an Edit
(`gplay edits commit`, and each write command in implicit mode) accepts two
opt-ins, still `[experimental]`:

- `--changes-in-review error` makes the commit fail instead, leaving the
  review untouched (the Edit stays valid). `--changes-in-review cancel` asks
  for Google's default explicitly.
- `--changes-not-sent-for-review` commits without sending the changes for
  review; they wait until someone sends them from the Play Console.

```sh
gplay metadata apply --confirm --changes-in-review error
```

In explicit mode the write commands do not commit, so pass these flags to
`gplay edits commit`; a write command given them while an Edit is pinned
warns and ignores them.

## When a commit's outcome is unknown

A commit that times out or gets a 5xx may still have gone through. gplay
keeps the network or upstream exit code (`50` or `40`) but marks the failure
`COMMIT_OUTCOME_UNKNOWN`, not retryable: check the live state before running
the command again. See [Exit codes](/docs/concepts/exit-codes/).

## Related

- [Tracks & releases](/docs/concepts/tracks-and-releases/)
- [`gplay edits` reference](/docs/reference/edits/)
- [`gplay releases` reference](/docs/reference/releases/)
