# ADR-0052: `--plan` previews a release transition from the live track

## Status

accepted (2026-11-09, #740)

## Context

`gplay releases complete` can retire the previous full rollout of a track
(#734), and `rollout`, `halt` and `resume` each pick one release among those
that coexist. Users asked to see that choice before sending it. `--dry-run`
cannot answer: it is documented as "without any HTTP call", part of the
Public contract (ADR-0010, ADR-0042), and offline CI relies on it.

## Decision

The four state transitions gain `--plan`, an online, read-only preview:

- It reads the track inside the pinned explicit Edit when there is one (so
  it sees the changes staged there), else inside a read-only Edit that it
  discards, best effort, as `releases list` does (`edits.WithReadOnlyEdit`).
  It never sends `tracks.update` and never commits.
- It reports the target release, the status and fraction it would end with,
  and the releases it would supersede and keep. These come from the same
  `pickTargetRelease` and `superseded` the write path uses, so a plan cannot
  disagree with the update it previews.
- No `--confirm` is needed, because nothing reaches users. An ambiguous or
  unmatched selection fails with the live path's exit code (60, 30).
- `--output json` is gplay-shaped (`plan`, `track`, `target`, `status`,
  `userFraction`, `superseded`, `kept`), never a `tracks.update` body, and
  carries `"plan": true`.
- `--plan` with `--dry-run` is a usage error (exit 2).
- Like `--dry-run`, `--plan` runs under `GPLAY_READONLY` (ADR-0024): it
  writes nothing.
- The flag is experimental on otherwise frozen commands
  (`kernel.ExperimentalFlag`), so its output can still change.

`--dry-run` keeps its offline promise unchanged.

## Alternatives rejected

- **Make `--dry-run` online.** It changes a frozen contract: auth becomes
  required and offline CI breaks.
- **Online when credentials resolve, offline otherwise.** The output would
  depend on the environment, which is hard to reason about in CI.

## Consequences

`promote` stays out. It sends its release through the typed `tracks.update`,
not a patch of the destination track, so what it does to the releases
already on the destination is Play's call, not a rule gplay owns. A plan for
it would state a guess.
