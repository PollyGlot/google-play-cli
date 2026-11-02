# Production track releases default to `draft`, other tracks default to `completed`

When `gplay releases upload` or `gplay releases promote` targets the `production` track, the resulting release is created in `draft` status unless the caller passes `--complete`, `--staged <fraction>`, or `--draft` explicitly. On every other track (`internal`, `alpha`, `beta`, custom closed tracks), the default is `status=completed` — i.e. published 100% immediately, with no `userFraction` on the release (see the [2026-11-02 amendment](#amendment-2026-11-02-731-733-a-completed-release-carries-no-fraction)).

The rationale is asymmetric blast radius. A faulty internal/alpha release is seen by a handful of testers and is fixed by uploading another build. A faulty production release reaches every user of the app and is costly to roll back. Making the production publish path require an explicit, named opt-in (`--complete` or `--staged 0.05`) turns "I forgot a flag" into a no-op rather than a 100% rollout.

## Considered Options

- **Fastlane-style defaults (`completed` everywhere)** — rejected. Parity with `fastlane supply` is nice but the cost of accidental 100% rollouts in CI scripts is too high. A migrating user gets a clear error message ("production releases require `--complete` or `--staged X`") instead of a silent ship.
- **Strict (`draft` everywhere)** — rejected. Internal/alpha tracks are explicitly meant for "push and let testers see it" loops; requiring `--complete` on every internal upload would just train people to alias it away and defeat the safety on production.

## Consequences

- The pattern applies to **every command that creates or moves a release**. `upload`, `promote`, and any future command (e.g. `releases stage`, `releases set-status`) must follow the same default rule so users only learn it once.
- Documented in `--help` of every affected command, not just the top-level docs — users discover the rule at the moment they need it.
- Migration note for ex-Fastlane users belongs in `docs/CI_CD.md` (or equivalent) since it's the single most likely source of "why didn't my CI publish?" tickets.

## Amendment (2026-11-02, #731, #733): a completed release carries no fraction

This ADR first wrote the non-production default as
`status=completed, userFraction=1.0`, and gplay sent exactly that. Google Play
rejects it. `TrackRelease.userFraction` in
`docs/discovery/androidpublisher_v3.json` can only be set on an `inProgress`
or `halted` release, and only in `0 < fraction < 1`. A completed release is a
full rollout by definition and sends no `userFraction` at all.

The decision above stands; only its wire shape is corrected:

- `--complete` sends `status=completed` with no fraction, on every command
  that sets it (`releases upload`, `releases promote`, `releases complete`).
- `--staged` accepts `0 < fraction < 1` only. `--staged 1.0` is a usage error
  (exit 2) that points to `--complete` (to `gplay releases complete` on
  `releases rollout`). Play has always refused that value, so no invocation
  that used to succeed stops working.
