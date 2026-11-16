# ADR-0054: `releases promote` reads its destination, and release dry-runs carry `dryRun` and `requires`

## Status

accepted (2026-11-16, #785), amends [ADR-0052](./0052-release-transition-plan.md) (its Consequences paragraph on `promote`; its Decision stands)

## Context

`gplay releases promote` sent the destination track with the promoted release
alone and never read it first. `tracks.update` replaces the whole releases
array, so a halted or draft release already on `--to` disappeared without a
word. ADR-0052 kept `promote` out of `--plan` on the ground that this
replacement is Play's call. It is gplay's payload: gplay chooses to send one
release, and the releases array it sends is the one Play keeps.

The same audit found three smaller gaps. `--from` equal to `--to` was accepted.
The source release's `countryTargeting` was dropped by the typed rebuild. And
the dry runs of `rollout`, `halt`, `resume`, `complete` and `promote` printed
neither `"dryRun": true` nor the ADR-0017 `requires` array, and showed
`versionCode: 0` even with `--version-code`.

## Decision

1. **`promote` reads `--to` inside its Edit** before the `tracks.update`, through
   the same helper for the write and the plan. The write still replaces every
   release on `--to`; the `✓` line names the versionCodes it replaced.
2. **`promote` gains `--plan`**, on ADR-0052's terms: online, read-only, inside
   the pinned explicit Edit or a discarded read-only Edit, no `--confirm`,
   exclusive with `--dry-run`, experimental. It reports the source release, the
   state the copy lands in, and the releases on `--to` it replaces. JSON:
   `plan`, `from`, `to`, `source`, `status`, `userFraction`, `replaced`.
3. **`promote --dry-run` stays offline**, as ADR-0052 keeps every `--dry-run`. It
   cannot name the releases on `--to`, so it states the replacement without
   inventing content: `"replacesDestination": true` in JSON, a `replaces:` line
   in the table pointing at `--plan`.
4. **`--from` equal to `--to` is CLI misuse** (exit 2), refused before any HTTP.
   Changing a release in place is the rollout state machine's job.
5. **`countryTargeting` carries over verbatim when the copy is a staged
   rollout.** It follows the fraction, as in the rollout state machine (#779):
   Discovery allows it only on an `inProgress` release, so a draft or completed
   copy goes without it rather than sending a body Play refuses.
6. **Release dry-runs carry `"dryRun": true`, `requires` and the requested
   `versionCode`** (`rollout`, `halt`, `resume`, `complete`, `promote`).
   `requires` is `["confirm"]` when the live run would reach production users,
   else `[]`. Additive under ADR-0003: no existing field changes name or type;
   `versionCode` stays `0` when `--version-code` is not passed.

## Alternatives rejected

- **Keep the replacement and only document it.** The operator still cannot see
  which releases go before they go.
- **Merge the promoted release into the destination's releases**, as the rollout
  verbs patch theirs. It changes what promote does on Play, and which
  combinations Play accepts on one track is not observed offline.
- **Make `promote --dry-run` read the destination.** It breaks the offline
  promise ADR-0052 keeps.

## Consequences

- Each live promote costs one more `tracks.get`. A 404 on it carries the same
  `gplay tracks create <name>` hint as a 404 on the `tracks.update`.
- Play's handling of the replaced releases (whether a halted rollout's users
  keep their build, for instance) is not observed offline; gplay reports what
  it sends, not what Play does with it.
- `upload --dry-run` carries `dryRun` but not yet `requires`; it is outside
  this decision.
