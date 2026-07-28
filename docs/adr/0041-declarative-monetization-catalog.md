# Declarative monetization catalog: pull/apply reconciliation, legacy-union pull, gated one-way migrate

## Status

accepted

## Context

[ADR-0026](./0026-maximal-admin-api-coverage.md) puts the whole Play
**admin** API in scope, and the monetization block is its largest unbuilt
continent (~54 methods): `monetization.subscriptions` (+ `basePlans` +
`offers`), `monetization.onetimeproducts` (+ `purchaseOptions` + `offers`),
the legacy `inappproducts`, and `monetization.convertRegionPrices`. The
grilling of PRD [#51](https://github.com/PollyGlot/google-play-cli/issues/51)
(2026-07-20) settled how gplay owns this surface; this ADR records those
decisions. Slice [#367](https://github.com/PollyGlot/google-play-cli/issues/367)
ships the walking skeleton (subscription top level); later slices
([#368](https://github.com/PollyGlot/google-play-cli/issues/368)–[#372](https://github.com/PollyGlot/google-play-cli/issues/372))
extend the same engine.

Three realities shape the design:

1. **The catalog is config.** Subscriptions and one-time products are
   slow-moving, reviewable state that belongs next to the code — like the
   Store front gplay already owns declaratively (`metadata`,
   [ADR-0011](./0011-metadata-apply-sync-model.md)). Imperative CRUD
   (28 create/patch/delete-ish methods across the nesting) is the wrong
   abstraction for a repo-as-source-of-truth workflow.
2. **Editing config must never reprice a paying subscriber.** The API
   splits "what new buyers see" (subscription/basePlan/offer fields) from
   "what existing subscribers pay" (`basePlans.batchMigratePrices`). A
   config apply that silently triggered a price migration would be a
   money-moving side effect.
3. **Legacy `inappproducts` still holds real catalogs.** Google
   auto-migrated one-time products to the v2 model only for Console-only
   accounts; any account that ever wrote via the `inappproducts` API keeps
   unmigrated products that are **invisible to the v2 list**. gplay's users
   are by definition API-managed accounts.

## Decision

1. **Declarative reconciliation, mirroring `metadata sync`.** Two core
   verbs per catalog namespace (`subscriptions`, later `iap`):
   - **`pull --package P --dir D`** writes the live catalog as one JSON
     file per product (`<productId>.json`), each file the API resource
     verbatim (wire format, [ADR-0003](./0003-json-passthrough.md) spirit)
     minus server-derived output-only noise.
   - **`apply --package P --dir D`** computes the create/patch/delete set
     (files vs live), prints it as the plan, and executes it.
     `--dry-run` prints the plan and stops. `pull` then `apply` with no
     edits is a no-op.
2. **Mirror stance, not additive** — unlike ADR-0011. A catalog directory
   is the **complete** declared catalog: a live product with no file is a
   **delete** in the plan. The metadata tree's additive stance exists
   because locale files are partial views; a monetization catalog is a
   closed set whose omissions must be visible, not ignored. The safety
   valve is the gate, not silence:
3. **Destructive plans are gated.** `apply` executes creates and patches
   directly (still mutating, marked as such), but any plan containing a
   **delete refuses to run without `--confirm`** (exit 3) — the
   `orders refund` / `customapps create` gate family. `--dry-run` always
   shows the full plan first.
4. **`apply` never touches an existing purchaser.** Editing a price in a
   file affects **new** purchases only. Migrating existing subscribers
   (`batchMigratePrices`) is the sole imperative escape hatch — its own
   gated command (slice #370), never triggered by an `apply` diff.
5. **Scoped diff, scoped `updateMask`.** Each slice reconciles only the
   fields it owns and sends an `updateMask` limited to them — the walking
   skeleton patches `listings`, `taxAndComplianceSettings` and
   `restrictedPaymentCountries` and therefore cannot clobber `basePlans`
   it does not yet manage. Nested levels join the diff as their slices
   land, not before.
6. **`--regions-version` with a pinned default.** `create`/`patch` require
   Google's regions version string; gplay defaults to the current
   published version (`2022/02`) and exposes `--regions-version` to
   override when Google publishes a new one — a flag, not a config knob,
   so the pin is visible in CI logs.
7. **v2 for all writes; `pull` unions legacy.** (`iap` slices #371–#372.)
   `iap apply` writes `monetization.onetimeproducts` only. `iap pull`
   reads **both** v2 and legacy `inappproducts` and unions by product ID,
   so unmigrated legacy products are never invisible. Editing a
   legacy-origin product requires the explicit one-way **`--migrate`**
   flag, which promotes it to v2 on apply; without it, `apply` errors.
8. **`convertRegionPrices` folds into pricing** (slice #368), not a
   standalone command.

## Consequences

- The reconciliation engine (file-set vs live-set diff keyed by product
  ID, plan printing, gate policy) is written once in slice #367 and reused
  by every later slice; nesting extends the diff, it does not fork the
  model.
- `--output json` on `apply` emits the plan (a gplay-owned shape,
  `[experimental]` until graduation); on `pull` the files themselves are
  the API pass-through.
- Deleting a subscription is further guarded server-side (Google refuses
  to delete a subscription with a published base plan), so `--confirm`
  gates intent, not just damage.
- The `archived` subscription state is **not** reconciled: the current
  Discovery snapshot marks subscription archiving deprecated/output-only,
  so declared state is create/patch/delete only.

## Alternatives rejected

1. **Imperative CRUD commands** (`subscriptions create/update/delete …`).
   Rejected: 28 imperative verbs across 3 nesting levels, none of which
   answer "is live drifting from the repo?" — the question the surface
   exists to answer.
2. **Additive sync (ADR-0011 stance).** Rejected: omission-blindness on a
   closed catalog hides deletes forever; monetization needs mirror
   semantics with a gate, not additive semantics with `--prune`.
3. **`apply` cascading into price migration when a price changes.**
   Rejected: money-moving side effect; migration stays a separate gated
   imperative (#370).
4. **v2-only `pull`.** Rejected: strands unmigrated legacy one-time
   products invisible to the v2 list — exactly the catalogs of API-managed
   accounts.
5. **Auto-migrating legacy products on any edit.** Rejected: legacy→v2 is
   a one-way door; it must be an explicit per-run decision (`--migrate`),
   in the same gate family as the other irreversible acts.
