# Production track releases default to `draft`, other tracks default to `completed`

When `gplay releases upload` or `gplay releases promote` targets the `production` track, the resulting release is created in `draft` status unless the caller passes `--complete`, `--staged <fraction>`, or `--draft` explicitly. On every other track (`internal`, `alpha`, `beta`, custom closed tracks), the default is `status=completed, userFraction=1.0` — i.e. published 100% immediately.

The rationale is asymmetric blast radius. A faulty internal/alpha release is seen by a handful of testers and is fixed by uploading another build. A faulty production release reaches every user of the app and is costly to roll back. Making the production publish path require an explicit, named opt-in (`--complete` or `--staged 0.05`) turns "I forgot a flag" into a no-op rather than a 100% rollout.

## Considered Options

- **Fastlane-style defaults (`completed`/`1.0` everywhere)** — rejected. Parity with `fastlane supply` is nice but the cost of accidental 100% rollouts in CI scripts is too high. A migrating user gets a clear error message ("production releases require `--complete` or `--staged X`") instead of a silent ship.
- **Strict (`draft` everywhere)** — rejected. Internal/alpha tracks are explicitly meant for "push and let testers see it" loops; requiring `--complete` on every internal upload would just train people to alias it away and defeat the safety on production.

## Consequences

- The pattern applies to **every command that creates or moves a release**. `upload`, `promote`, and any future command (e.g. `releases stage`, `releases set-status`) must follow the same default rule so users only learn it once.
- Documented in `--help` of every affected command, not just the top-level docs — users discover the rule at the moment they need it.
- Migration note for ex-Fastlane users belongs in `docs/CI_CD.md` (or equivalent) since it's the single most likely source of "why didn't my CI publish?" tickets.
