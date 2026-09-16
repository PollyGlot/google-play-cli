# Coverage dispositions: redundant and parked, declared in the registry

## Status

accepted, amends [ADR-0026](./0026-maximal-admin-api-coverage.md)

## Context

[ADR-0026](./0026-maximal-admin-api-coverage.md) made every Play admin API
in scope and left exactly one way out: a *runtime* API, excluded by nature
in `internal/apiregistry/exclusions.go`. Since [PRD #513](https://github.com/PollyGlot/google-play-cli/issues/513)
`docs/COVERAGE.md` is rendered from the registry plus that exclusion list,
and every other method is *uncovered* by subtraction.

After the 1.5.0 sweep (#541 to #545) the table still showed 43 🔴 rows, and
none of them was a real gap. They fell into two families the model could not
express:

- **Redundant** (32): the same resource in another shape, already served by
  a method a shipped command calls. `update` next to a called `patch`
  (details, listings) or `patch` next to a called `update` (testers, tracks,
  expansion files); `get` and `batchGet` next to a called `list`; `batch*`
  writes next to the per-item call the declarative `apply` drives; the eight
  legacy `inappproducts` writes gplay never issues
  ([ADR-0041](./0041-declarative-monetization-catalog.md) §8); the
  deprecated `subscriptions.archive`; and the `batchUpdateStates` verbs
  whose unary `activate`/`deactivate`/`cancel` siblings are the ones called.
- **Parked** (11): a decision deferred to a `type:parking` issue
  (#546, #295, #352, #346, #296, #348). The tracker knew, the registry did
  not, so the row could not point at the issue.

A table that marks 43 methods as gaps when zero are is lying by omission, on
the repo and on the site. The alternative of folding them into exclusions
would lie the other way: "excluded by nature" is a narrow claim ADR-0026
reserves for runtime APIs.

## Decision

`internal/apiregistry` carries **four** hand-written answers, one per
method, and `docs/COVERAGE.md` renders five states:

| State | Declared in | Meaning |
|---|---|---|
| called | `registry.go` | at least one shipped command resolves the method through the registry |
| excluded | `exclusions.go` | runtime by nature, never wrapped (unchanged from ADR-0026) |
| **redundant** | `dispositions.go` | same resource, another shape; names the **canonical** method a shipped command calls |
| **parked** | `dispositions.go` | decision deferred; names the `type:parking` **issue** |
| uncovered | (by subtraction) | in scope, no command, no disposition: a real gap |

1. **A redundant entry is honest only while its canonical is called.**
   `Redundant{MethodID, CanonicalID, Reason}` is linted offline: the
   canonical must be a registered entry. If the canonical ever loses its
   command, `go test ./internal/apiregistry/` fails and the method is a gap
   again. Without that invariant the disposition would be cosmetic.
2. **A parked entry is a traced gap, not a covered one.** `Parked{MethodID,
   Issue, Reason}` keeps the 🔴 mark; the row links `#<issue>`. The issue
   must be strictly positive, the id must exist in `paths.txt`, and the
   method must carry no other disposition.
3. **The denominator does not move.** Redundant and parked methods stay in
   the admin count and get their own headline columns; they are not folded
   into "called". The headline reads "X called, Y redundant, Z parked, N
   uncovered", and N is expected to be 0.
4. **A bare 🔴 row fails a test.** `internal/coveragedoc` checks the
   committed file for an uncovered row with no issue link. A method Google
   adds on the Monday Discovery refresh lands as a bare 🔴 and turns CI red
   until it is registered, excluded, declared redundant or parked: the
   refresh PR names the decision to take.
5. **Choosing the canonical.** The canonical is the method the shipped
   command actually resolves on the *same* resource, so a maintainer can
   read the pair and agree in one glance. A pair that cannot be stated that
   plainly is parked behind an issue, not declared redundant.

## Consequences

- `docs/COVERAGE.md` shows 0 uncovered methods after this ADR: 127 called,
  32 redundant, 11 parked, 11 excluded, of 181.
- One more Go file to maintain by hand, with the same offline anchors as
  the registry (`paths.txt`, duplicate detection, one disposition per id).
- `basePlans.batchUpdateStates`, `basePlans.offers.batchUpdateStates` and
  `purchaseOptions.offers.batchUpdateStates` are redundant with the unary
  state verbs `subscriptions apply` and `iap apply` drive (#551);
  `purchaseOptions.batchUpdateStates` stays called because that
  sub-resource has no unary verb. ADR-0041 records this.
- `edits.listings.deleteall` is redundant with `edits.listings.delete`
  (`metadata apply --prune` deletes locale by locale and refuses the
  defaultLanguage Listing, [ADR-0011](./0011-metadata-apply-sync-model.md)):
  it is not parked, because no parking issue covers it (#521 covers
  `edits.images.deleteall`, which is called).

## Alternatives rejected

1. **Widen `Exclusion` with a kind field.** Rejected: "excluded by nature"
   is the one claim ADR-0026 lets the registry make without an issue, and
   diluting it would let an unshipped method hide as excluded.
2. **Fold redundant methods into "called".** Rejected: the table would
   claim coverage the code does not have, the exact drift PRD #513 removed.
3. **Keep the 43 as bare 🔴 and explain them in prose.** Rejected: prose
   drifts, the registry is the only input a test can anchor to.
