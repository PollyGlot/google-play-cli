# Declarative monetization catalog: pull/apply reconciliation, legacy-union pull, gated one-way migrate

## Status

accepted

amended (2026-11-20): implementation decisions moved from code comments

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
   file affects **new** purchases only. Migrating existing subscribers is
   the sole imperative escape hatch — **`subscriptions prices migrate`**
   (slice #370): one base plan per invocation via `basePlans.
   migratePrices`, `--region`-scoped with an `--oldest` cohort cutoff,
   DESTRUCTIVE tier (`--confirm`, exit 3; offline `--dry-run`;
   MarkMutating), never triggered by an `apply` diff (pinned by test).
   The batch sibling (`batchMigratePrices`) is deliberately not wrapped —
   no bulk money-moving, the ADR-0031 stance. `migrate` is a domain verb
   admitted under [ADR-0019](./0019-canonical-verb-vocabulary.md) §2.
5. **Scoped diff, scoped `updateMask`.** Each slice reconciles only the
   fields it owns and sends an `updateMask` limited to them — the walking
   skeleton (#367) patches `listings`, `taxAndComplianceSettings` and
   `restrictedPaymentCountries`. Nested levels join the diff as their
   slices land, not before. **`basePlans` joined with slice #368**: base
   plan *config* (billing type, per-territory `regionalConfigs` prices)
   is declarable and rides the parent subscription patch — the API has no
   create/patch on the sub-resource; its endpoints only manage **state**
   (`activate`/`deactivate`, slice #369) and **subscriber price
   migration** (#370). The output-only `basePlans[].state` subfield is
   therefore *normalized out of the diff* (a live `ACTIVE` never produces
   a phantom patch) while `pull` keeps writing it. **Offers joined with
   slice #369**: unlike base plans they are a real sub-resource with
   their own CRUD, so the catalog file *embeds* them
   (`basePlans[].offers`, a file construct the API resource does not
   carry — pull nests them from one wildcard `offers.list` walk, apply
   splits them back out and strips them from parent bodies) and
   reconciles them through the offers endpoints under the same composite
   key (`productId/basePlanId/offerId`).
6. **Lifecycle state is declarative, reconciled via the dedicated
   endpoints, prominent but ungated** (slice #369). A base plan's or
   offer's `state:` field declares `ACTIVE` or `INACTIVE`; apply
   reconciles a drift through `:activate`/`:deactivate` — never a patch.
   A missing `state:` declares nothing (missing = unmanaged, the
   metadata stance); an unreachable declaration (`DRAFT` from anything,
   `INACTIVE` from `DRAFT`) is a usage error naming the transition.
   State changes are listed prominently in every plan view — they move
   buyer availability — but are **not** gated behind `--confirm`:
   activate/deactivate are reversible, deletion is not.
7. **`--regions-version` with a pinned default.** `create`/`patch` require
   Google's regions version string; gplay defaults to the current
   published version (`2022/02`) and exposes `--regions-version` to
   override when Google publishes a new one — a flag, not a config knob,
   so the pin is visible in CI logs.
8. **v2 for all writes; `pull` unions legacy.** (`iap` slices #371–#372.)
   `iap apply` writes `monetization.onetimeproducts` only — a v2 create
   is `patch` with `allowMissing`, the API has no insert; offer writes
   ride the per-purchase-option batch endpoints. `iap pull` reads
   **both** v2 and legacy `inappproducts` and unions by product ID, so
   unmigrated legacy products are never invisible (a product live in
   both models keeps the v2 file — the legacy row is its pre-migration
   shadow). A file's origin is its shape (`sku` = legacy, `productId` =
   v2). The legacy surface is **inert**: never created, edited or
   deleted; the promotion gesture is rewriting the file in the v2 schema
   and applying with the explicit one-way **`--migrate`** flag (exit 3
   without it, its own `migrate` op in the plan) — once promoted, a
   product can never return to `inappproducts`.
   **Amended 2026-11-09 (#741).** Google refuses the legacy list for an app
   whose catalog it moved to v2, with a 403 whose only discriminant is its
   message ("Please migrate to the new publishing API"). That refusal means
   the legacy inventory is empty, not unreadable: `pull` and `apply` read v2
   only and note it on stderr. A legacy file in the catalog is then refused
   (its product now lives in v2, and planning without it would delete that
   v2 product) until `pull` rewrites it. Any other 403 still fails as a
   permission error.
9. **`convertRegionPrices` folds into pricing** (slice #368), not a
   standalone top-level command: **`subscriptions prices convert --price
   --currency`** derives per-region prices from one base price, printing
   the `ConvertRegionPricesResponse` verbatim so its Money objects paste
   into a catalog file's `regionalConfigs`. A computation, not a write —
   not marked mutating. `convert` is a domain verb admitted under
   [ADR-0019](./0019-canonical-verb-vocabulary.md) §2 (Google's own
   method name; no canonical verb reads as "compute derived prices").

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
- A base plan dropped from its subscription's file is a `basePlan` delete
  in the plan (slice #542), executed through `basePlans.delete` after the
  parent patch: the patch alone never removes a plan its body omits.
  Discovery contradicts itself on which plans the API deletes:
  `basePlans.delete` says "Can only be done for draft base plans", while
  `BasePlan.state` says "Draft and inactive base plans can be activated or
  deleted". Both readings refuse an ACTIVE plan, so the plan refuses
  dropping a plan the list shows ACTIVE (exit 2, before any write) with
  the hint that converges in both, keep it in the file with
  `state: INACTIVE` (#778). An INACTIVE plan is sent to the API, which
  settles the contradiction: gplay never blocks a delete Play might
  accept. A refusal (400/403) is reported through the ADR-0044 envelope
  (operation `monetization.subscriptions.basePlans.delete`, the same hint)
  *after* running the plan's other changes, since it is independent of
  them.
- An offer dropped from the file follows the same rule (#790).
  `offers.delete` says "Can only be done for draft offers" and
  `SubscriptionOffer.state` says nothing about deletion, so no Discovery
  reading deletes an ACTIVE offer: the plan refuses dropping one the list
  shows ACTIVE (exit 2, before any write), with the hint to keep it under
  its base plan with `state: INACTIVE`. An INACTIVE offer is still sent to
  the API: Discovery words `basePlans.delete` identically and contradicts
  it elsewhere, so the sentence alone is not proof that Play refuses it. A
  refusal is reported last like a base plan's, with its own diagnostic
  code, `OFFER_NOT_DRAFT` on the 400 (ADR-0044).
- Offer and base-plan state changes ride the **unary** verbs
  (`activate`/`deactivate`/`cancel`, one call per item, slice #551); the
  `batchUpdateStates` siblings are declared redundant in the registry
  ([ADR-0047](./0047-coverage-dispositions-redundant-and-parked.md)). Only
  `purchaseOptions.batchUpdateStates` is called, that sub-resource having no
  unary verb.
- The `archived` subscription state is **not** reconciled: the current
  Discovery snapshot marks subscription archiving deprecated/output-only,
  so declared state is create/patch/delete only.

## Implementation notes

- **`pull` strips exactly two fields**: `packageName` (redundant with
  `--package`) and `archived` (output-only in the current Discovery
  snapshot), the noise that would churn review diffs without being declarable
  state. It removes every stale `*.json` file and never touches a file of any
  other extension. Files are written through `output.WriteJSON`, so listing
  copy keeps its `<`, `>` and `&` ([ADR-0003](./0003-json-passthrough.md)).
- **Plan views list the plan in the order `apply` runs it**: grow (creates,
  patches, offer creates and patches), then state changes, then shrink (offer
  deletes, base-plan deletes, product deletes). Nothing but this rule ties the
  two; a change to one changes the other.
- **The `--output json` plan is one flat list of changes**, so a CI gate is
  one `jq` line. The middle level of an identity sits under its catalog's own
  key (`basePlanId` for subscriptions, `purchaseOptionId` for one-time
  products), only one of which is ever set. A migrating product create is
  shown as its own `migrate` op in every view, so the one-way door is visible.
- **A change carries a typed identity** (product, middle level, offer), never
  a composite string split again later, and a patch's changed fields are both
  the summary shown and the exact `updateMask` sent.
- **Each catalog has its own directory**: `./monetization/subscriptions` and
  `./monetization/iap` by default, so the two file sets never interleave.

### Empty-side guards

- **An empty declared catalog facing a non-empty live one is refused**, even
  under `--dry-run` (exit 2): it is far more likely a mis-pointed `--dir` than
  an intent to delete the whole catalog. This ports the `metadata --prune`
  empty-tree guard ([ADR-0011](./0011-metadata-apply-sync-model.md)).
- **`pull` refuses the mirror image.** Mirror semantics make `pull`
  destructive locally, so an empty live listing (a mis-set `--package`, a lost
  scope) facing a populated catalog directory is refused (exit 2) instead of
  erasing the files.

### Diff scope

- **`purchaseOptions` ride the product patch.** The sub-resource has only
  `batchDelete` and `batchUpdateStates`, so its config is declared in the
  product file and patched with it, like base plans; its output-only `state`
  is reconciled through `batchUpdateStates` and kept out of the diff, so a
  state change never doubles as a phantom patch.
- **The one-time offer's managed fields are pinned to Discovery** by a test
  against the embedded Schema index. A declarable field missing from the list
  would be invisible to both the diff and the `updateMask`, a silent no-op on
  patch, so a field Google adds fails the test until it is managed or listed
  with its reason. Identity, the server-stamped `regionsVersion` and `state`
  are never managed.
- **An offer whose base plan is missing from its subscription is an
  integrity error** when `pull` nests offers, never a silent drop.
- **Children of a deleted parent are not planned.** Deleting a subscription
  takes its base plans and offers, and deleting a one-time product takes its
  purchase options and offers, so the plan carries no individual delete or
  state change for them.

### Execution and failure

- **Execution is not transactional.** No batch spans the verbs a plan uses;
  a failure surfaces at once and a re-run converges. The one-time catalog runs
  product upserts, offer batch upserts, offer batch deletes, then product
  deletes, with state moves after the upserts so a resource created in the
  same run can be activated in it.
- **A refused subscription offer or base-plan delete is recorded, not
  fatal.** It is independent of every other change (an INACTIVE one Play will
  not delete, or one published after the list), so the run finishes the plan
  and reports it last. Only that refusal (a 400, or a 403 on some accounts)
  is recorded; a transport failure, a 5xx or a 404 aborts like any other
  write.
- **The 400 carries its own diagnostic code, the 403 does not.** On the 400
  the envelope says `OFFER_NOT_DRAFT` or `BASE_PLAN_NOT_DRAFT` (exit 30), so
  an agent branches on "keep it as INACTIVE" without parsing the message; the
  wrapped API error keeps the exit code, the operation and the upstream
  reasons. A 403 stays `PERMISSION_DENIED` (exit 11): it may be the same
  refusal, but it cannot rule out a genuine missing grant. The hint travels in
  the message either way.
- **Among several recorded refusals, a 403 drives the final error**, whatever
  the order. A missing grant is not resolved by the file and a re-run would
  hit it again, while a state refusal's hint is already on stderr. This
  refines [ADR-0040](./0040-variadic-apps-add-partial-failure.md)'s
  first-non-retryable rule; the count still names the other refusals.

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
