# 1.0 GA: freeze what is proven, label the rest — the stability mechanism that makes the Public contract enforceable

## Status

accepted

## Context

[ADR-0010](./0010-versioning-public-contract-and-ga.md) settled the *policy* of
`v1.0` back in May 2026: a stability promise rather than a feature checklist, a
granular contract scoped by per-command stability labels, and a `1.0` that
freezes the proven surface without waiting for Fastlane parity. What it never
got was a *mechanism*. The labels existed as hand-written `[experimental]`
prefixes copy-pasted into 35 command files — a convention, not a registry:
nothing could read it, nothing enforced it, and nothing failed when a new
command shipped without one.

The trigger for cutting `1.0` was not a decision. [#410](https://github.com/PollyGlot/google-play-cli/issues/410)
landed as a `fix!` (missing `--confirm` refusals exit 3, not 2) and
release-please computed `1.0.0`, because — contrary to what
[ADR-0008](./0008-release-pipeline.md) §versioning claimed — a breaking change
in `0.y.z` bumps the **major** unless `bump-minor-pre-major` is set, which this
repo never set. That surfaced the real question: is the project ready for the
promise the version number implies?

The state at the time of the cut:

- **~114 of ~159 admin methods shipped** across 21 namespaces
  ([COVERAGE.md](../COVERAGE.md)), including the whole MVP surface ADR-0010
  named as the freeze target.
- **Everything still unbuilt is additive** — new namespaces (`appstore`,
  `appstorecatalog`), not changes to existing commands. `1.x` absorbs them.
- **The verb vocabulary was settled** in [ADR-0019](./0019-canonical-verb-vocabulary.md)
  and is enforced by `verb-gate` — the rename debt ADR-0010 flagged as owed
  before the freeze is paid.
- **But the newest surface is days old.** The declarative monetization catalog
  ([ADR-0041](./0041-declarative-monetization-catalog.md)) shipped in `v0.18.0`
  on 2026-07-29, three days before the cut, and exposes a file schema and a
  reconciliation model as contract.

Freezing all of that at once is exactly the false choice ADR-0010 rejected.

## Decision

1. **Cut `1.0.0`.** The Public contract enters force for every command not
   labelled `[experimental]`. The maturity state communicated everywhere
   (README, website, badges) moves from *public preview* to *GA*.

2. **The stability label becomes a cobra annotation, not a string convention.**
   `kernel.Experimental(cmd)` sets `gplay.stability=experimental`, decorates the
   command's `Short` and `Long`, and walks the subtree so one call covers a
   namespace. `kernel.IsExperimental(cmd)` reads it back, walking up the parent
   chain so a subcommand can never be more stable than its namespace. This is
   the same shape as `MarkMutating` (ADR-0024) and `WithScope` (#49): declared
   once per command at registration in `cmd/gplay/main.go`, read in one place.

3. **Absence of a label means frozen.** Forgetting to label over-promises
   rather than under-promises — the failure mode a user can plan around.

4. **The classification is pinned by an exhaustive test.**
   `TestStabilityRegistry_pinsPublicContract` walks every runnable leaf of the
   real command tree and fails on any leaf that is not explicitly classified.
   A new command cannot join the frozen contract by omission.

5. **What ships `[experimental]` at 1.0:** `schema`, `orders`, `subscriptions`,
   `iap`, `games`, `recovery`, `device-tiers`, `customapps`, `releases sharing`,
   `releases expansion-files`, `releases generated`, and `reviews history`.
   Everything else is frozen. The criterion applied to each: *would a change to
   this command's flags or semantics justify a major bump six months from now?*
   Where the honest answer was "we would want to change it", it is labelled.

6. **Sub-feature labels stay prose.** APK upload on `releases upload`, the icon
   fields on `apps view`, and `--type` on `metadata images list` are
   experimental *parts* of frozen commands. A command-level annotation cannot
   express that, and inventing a flag-level one for three cases is not worth the
   machinery — they stay marked in the help text.

7. **Graduation is additive.** Dropping the label is not a breaking change and
   ships in a normal minor. The reverse never happens: a frozen command that
   must change is a major bump.

## Why

1. **A contract nobody can verify is not a contract.** The hand-written
   convention had no reader. `gplay orders` carried the marker in its `Short`
   while `orders` was, as far as any tooling could tell, frozen. One annotation
   plus one exhaustive test turns a copy-paste habit into something CI enforces.

2. **The label is what makes 1.0 honest rather than premature.** Without it the
   choice is to freeze a three-day-old file schema or to delay GA for months.
   With it, gplay can promise the release loop — the thing people actually wire
   into CI — while keeping the commerce continent free to move.

3. **Freezing the proven surface costs nothing and signals a lot.** `auth`,
   `apps`, `releases`, `tracks`, `reviews`, `metadata`, `team`, `compliance`
   have not changed shape in months. The promise merely states what is already
   true, and `0.x` was actively telling CI authors not to depend on it.

4. **The trigger being mechanical does not make the timing wrong.** The
   exit-code fix that forced the major is itself evidence the contract had
   started to matter: it was worth breaking precisely because callers branch on
   exit codes.

## What we lose

- **No dogfooding evidence behind the promise.** ADR-0010's readiness checklist
  (#98) wanted N releases through a real Android CI first. That box is not
  ticked; the repo has 5 stars and no external CI using it, so waiting would not
  have produced the evidence either. The label is the mitigation: the surfaces
  most likely to be wrong are the ones not being promised.
- **A wide experimental surface at GA.** Twelve namespaces ship unfrozen, which
  is a lot for a `1.0` and reads as hedging. The alternative — freezing them —
  is a promise that would be broken.
- **A graduation backlog.** Each experimental namespace now owes a decision
  later, tracked nowhere except this ADR and the registry test.

## Considered Options

- **Ship `1.0.0` as-is, freezing everything** — rejected. It would freeze the
  monetization catalog three days after it shipped, and the one-way `--migrate`
  promotion with it.
- **Set `bump-minor-pre-major: true`, cut `0.19.0`, plan GA later** — rejected,
  but it is the honest fallback if the labels are judged insufficient. It
  postpones a decision the surface is otherwise ready for, and "later" has no
  trigger that `0.x` will ever produce.
- **A flag-level stability annotation** — rejected as premature. Three
  sub-features need it today; prose covers them.
- **Wait for the `appstore` / `appstorecatalog` surfaces** — rejected. Both are
  new namespaces, purely additive, and land in `1.x` without a major bump.

## How this shows up to users

`--help` carries the label at every level: an experimental namespace is tagged
in its parent's command list, and its own help opens with a notice stating the
consequence — flags and behavior may change in any release, pin an exact release
if you depend on it in CI. The website documents the contract in
`docs/concepts/stability`, and `docs/guides/migrate-to-1-0` covers the single
breaking change at the boundary (exit 2 → 3 on missing `--confirm`).
