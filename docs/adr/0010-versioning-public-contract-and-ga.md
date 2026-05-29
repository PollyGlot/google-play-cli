# v1.0 is a stability boundary, not a feature checklist: a granular public contract gated by stability labels

[ADR-0008](0008-release-pipeline.md) owns the *mechanics* of versioning —
release-please decides the bump from Conventional Commits, GoReleaser ships the
artifacts — and states the `0.x` semver rule. This ADR pins the *policy* layered
on top: what `v1.0` actually promises, how that promise is scoped, and how the
project communicates maturity before and after it.

The decisions:

- **No GitHub milestones.** Versioning is owned end-to-end by
  release-please/SemVer from commit history. Scope is grouped by PRD issues and
  `type:*` / `area:*` labels, not by version-named milestones. Version-named
  milestones drift out of sync with auto-computed versions and rot — observed
  first-hand on the sibling `asc` project (App-Store-Connect-CLI), which carries
  a graveyard of stale `0.2x`–`0.3x` milestones while shipping `1.6.x`.
- **Two maturity states, communicated explicitly.** *Public preview* (`v0.x`):
  publicly installable, an invitation to test, breaking changes still possible.
  *GA* (`v1.0`+): the Public contract is in force.
- **`v1.0` freezes the current MVP surface** (auth, apps, releases, tracks,
  reviews) — it does **not** wait for Fastlane `supply` parity. Metadata,
  vitals, and subscriptions are additive and ship in `1.x` without a major bump.
- **The Public contract is granular, scoped by stability labels**, not a
  monolithic freeze (pattern borrowed from `asc`):
  - no label = part of the Public contract (frozen; breaking = major bump)
  - `[experimental]` = shipped but outside the contract, free to evolve
  - `DEPRECATED:` = a compatibility path kept during a migration
- **What the Public contract covers vs excludes:**
  - **Covered:** command and flag names + semantics, exit codes, config schema
    and resolution precedence, Account resolution precedence, and the guarantee
    that `--output json` stays API-passthrough.
  - **Excluded:** the `table` / `markdown` layouts (human views), the *fields*
    inside the passthrough JSON (owned by the Google Play Developer API, see
    [ADR-0003](0003-json-passthrough.md)), and stderr / log wording.
- **CLI `v1.0` and the companion skills launch jointly, version independently.**
  GA ships the CLI plus a v1 skill set (`gplay-setup`, `gplay-release-flow`,
  `gplay-reviews`, `gplay-tracks`) because the agent-first story is the
  differentiation — but each repo keeps its own SemVer.

## Why

1. **A stability promise, not a feature count.** `v1.0` answers "can I depend on
   this in CI without it breaking under me," not "does it do everything Fastlane
   does." Coupling the major bump to feature breadth means it never ships; a
   focused, honest 1.0 ("the core release loop, frozen") is more credible than a
   1.0 that waits for parity with a ten-year-old tool.

2. **The passthrough carve-out keeps the promise honest.** ADR-0008 loosely
   listed "JSON output shapes" among what `1.0` freezes; that is too strong and
   this ADR **refines that line**. Under [ADR-0003](0003-json-passthrough.md),
   `--output json` is the Google response verbatim — Google can change a field
   tomorrow. gplay can only promise *that json stays passthrough* (no envelope),
   not the field shape. Promising what you do not control is a broken promise
   waiting to happen.

3. **Stability labels let surface ship before it is frozen.** A monolithic
   freeze forces a false choice: delay 1.0 until every command is certain, or
   over-promise on the shaky ones. Per-command labels give an escape hatch and
   resolve the `tracks` read-only case cleanly — ship `tracks list`/`status`
   stable now, ship any future write subcommands `[experimental]` first, then
   graduate them.

4. **Joint launch carries the differentiation.** gplay's pitch is "native Go +
   agent-first + companion skills." A GA moment that is "just a CLI" leaves the
   differentiating story on the table. Versioning the two repos independently
   keeps the launch coupled without making the CLI's stability hostage to the
   skills repo's version number.

5. **Dropping milestones removes double-bookkeeping.** A PRD issue already groups
   its slices and closes when they are all done; a version-named milestone on top
   of that is a second ledger that release-please's auto-versioning immediately
   contradicts (it bumped to `0.2.0` while the "v0.1.0 — MVP" milestone was still
   the scope). One source of truth — commits for versions, issues for scope.

## What we lose

- **No at-a-glance "% to GA" bar** that a milestone gave for free. Replaced by a
  tracking issue (#98) plus a readiness checklist. Less visual, but it does not
  rot the way version-named milestones do.
- **A verb-rename debt to pay before 1.0.** Freezing the surface means the verb
  vocabulary must be made consistent first — today `apps info` inspects one app
  but `auth status` / `tracks status` inspect one of those, two verbs for one
  gesture. Renaming after the freeze is a breaking change. Tracked in #98.
- **Coupling the GA moment to a second repo.** The skills repo must be
  bootstrapped (#53) for the joint launch; if it slips, the launch slips. The
  independent versioning bounds the blast radius, but the dependency is real.

## Considered Options

- **Monolithic freeze at 1.0** — rejected. All-or-nothing forces either delaying
  1.0 until every command is certain or over-promising on shaky ones. Stability
  labels give a per-command escape hatch instead.
- **`1.0` = Fastlane `supply` parity** (wait for metadata/listings sync) —
  rejected. Couples a stability promise to feature breadth, so 1.0 never ships.
  Metadata is additive and lands in `1.x`.
- **Keep version-named milestones, align them by hand** — rejected. Fights
  release-please; the `asc` graveyard of stale milestones is the cautionary tale.
- **Ship CLI `1.0` alone, skills later** — rejected as the default. Workable, but
  the GA announcement loses the agent-first story that differentiates gplay.

## How this shows up to users

`gplay --help` and each subcommand's help carry the stability label (none /
`[experimental]` / `DEPRECATED:`), so a CI author can judge what is safe to
depend on before wiring it into a pipeline. A `migrate-to-1-0` guide will
document any surface that changes at the boundary. The README's preview banner
states the current maturity state; the version badge stays dynamic and no
version is hardcoded anywhere — including marketing images, whose terminal title
bar no longer carries a version string (consistent with ADR-0008's "nothing in
the README hardcodes a version").
