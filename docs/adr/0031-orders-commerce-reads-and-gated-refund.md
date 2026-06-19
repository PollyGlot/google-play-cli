# Orders surface: admin commerce reads + gated refund; voided-purchases deferred

## Status

accepted

## Context

[ADR-0026](./0026-maximal-admin-api-coverage.md) puts every Play **admin** API
in scope. The `orders` resource (`orders.get`, `orders.batchget`,
`orders.refund`) was split out of the long-tail PRD
[#243](https://github.com/PollyGlot/google-play-cli/issues/243) into its own PRD
[#245](https://github.com/PollyGlot/google-play-cli/issues/245) because it is the
**only money-touching surface** and deserves a focused write-safety grilling.

gplay already owns the primitives this surface needs, so this ADR composes them
rather than inventing new ones:

- **Write-safety tiers + exit 3** — destructive writes require a named
  acknowledgment flag; a missing flag is the resolvable exit `3`, not the
  malformed exit `2` ([ADR-0017](./0017-write-safety-and-agent-resolvable-refusals.md)).
- **Money permissions never bundled** — `CAN_VIEW_FINANCIAL_DATA` and
  `CAN_MANAGE_ORDERS` are expressible only as explicit Permissions, never folded
  into a Role bundle ([ADR-0016](./0016-permission-vocabulary.md)).
- **JSON pass-through** — `--output json` mirrors the API envelope verbatim
  ([ADR-0003](./0003-json-passthrough.md)).
- **Canonical read verb** — single-resource reads use `view`, not the
  pre-rename `get` ([ADR-0019](./0019-canonical-verb-vocabulary.md), verb-gate).

The remaining open question (flagged in `docs/BACKLOG.md` as "à trancher au
grilling de #245"): does `purchases.voidedpurchases.list` belong here? And one
boundary must be drawn concretely for commerce — order lookup vs purchase-token
verification.

## Decision

1. **`gplay orders` namespace, on the package/app axis.** The Discovery snapshot
   keys every `orders.*` method by `{packageName}`
   (`applications/{packageName}/orders/...`), so `orders` rides the app axis like
   `releases`/`metadata`, **not** the developer-account axis. A standalone
   namespace now; a future commerce umbrella (subscriptions/IAP,
   [#51](https://github.com/PollyGlot/google-play-cli/issues/51)) may reparent it
   while it is still `[experimental]`.

2. **Reads — `orders view <orderId> [<orderId>...]`.** One variadic read verb:
   a single ID calls `orders.get`; two to 1000 IDs call `orders.batchget` (the
   `orderIds` query, 1–1000 cap; over 1000 is exit `2`). `--output json` mirrors
   `Order` (single) / `BatchGetOrdersResponse` (batch) verbatim. Required Play
   permission: `CAN_VIEW_FINANCIAL_DATA`.

3. **Refund — `orders refund <orderId> --confirm [--revoke]`.** A money-moving,
   irreversible write (`orders.refund`, POST, no body) → the **destructive tier**
   (ADR-0017): `--confirm` is required, a missing flag is exit `3` naming it, and
   `CI=true` never auto-confirms. **No bulk / fan-out refund verb in v1** — the
   API is per-order, and a fan-out verb would multiply blast radius. `--revoke`
   (the API `revoke` query parameter) defaults **false** — refund the money but
   keep the entitlement; revoking access is the larger hammer, opt-in. Required
   Play permission: `CAN_MANAGE_ORDERS`, which is **never** part of a Role bundle
   (ADR-0016). A 403 on either read or refund surfaces as an agent-resolvable
   refusal naming the missing permission.

4. **Admin/runtime boundary for commerce, concretely.** Order lookup **by order
   ID** is admin: a human or agent holds an order ID from a user complaint or a
   payout report — no device token is involved — so it is in scope. Real-time
   **purchase-token verification** (`purchases.products`,
   `purchases.subscriptionsv2`) is runtime — an ephemeral device-issued token
   consumed server-side — and stays excluded by nature (ADR-0026). This is the
   canonical commerce example of the boundary, written into `CONTEXT.md` (term
   **Order**).

5. **`purchases.voidedpurchases.list` is in scope but deferred.** It is
   admin-leaning (a polled anti-fraud feed, no device token), so ADR-0026 keeps
   it in scope — but it is a **reconciliation** use case distinct from order
   lookup, and folding it into #245 would dilute the money-write grilling and
   pull in a `purchases.*` surface ahead of a real need. Recorded in
   `docs/BACKLOG.md` for a future commerce PRD adjacent to subscriptions/IAP
   (#51), **not** part of #245.

6. **Ships `[experimental]` first** ([ADR-0010](./0010-versioning-public-contract-and-ga.md));
   companion skill updated ([ADR-0021](./0021-companion-skills-repo.md)).

## Considered options

- **Separate `get` / `batch-get` verbs.** Rejected: `get` is a pre-rename verb
  blocked by verb-gate (ADR-0019), and two verbs for "read one or several
  orders" is redundant — a variadic `view` is one ergonomic surface that hides
  the get-vs-batchget routing.
- **A dedicated named flag for refund (à la `--grant-admin`).** Rejected: the
  verb `refund` already names the dangerous act, unlike `grants set --role admin`
  where the danger hides inside a value — so the generic `--confirm` is
  self-naming enough here (ADR-0017's reason for `--grant-admin` does not apply).
- **`--revoke` default true.** Rejected: revoking a user's access is more
  destructive than the refund itself; the safe default is money-only, with
  revocation opt-in.
- **Fold `voidedpurchases.list` into #245.** Rejected: different use case
  (reconciliation, not lookup); it would dilute the focused money grilling and
  expand the otherwise runtime-excluded `purchases.*` tree before there is a
  reconciliation need.
- **A bulk refund verb.** Rejected for v1: per-order is the API shape; bulk money
  movement is exactly the blast radius the write-safety tiers exist to contain.

## Consequences

- `orders` joins the experimental surface (ADR-0010). `--confirm`, `--revoke`,
  and exit `3` are documented in `orders refund --help`; the `requires` array
  appears under `--dry-run --output json`.
- `CONTEXT.md` gains the term **Order** and the canonical admin/runtime commerce
  boundary example; the avoid-list separates Order from *purchase token* and
  *voided purchase*.
- `docs/BACKLOG.md` records `voidedpurchases.list` as in-scope-but-deferred (no
  longer "à trancher au grilling de #245").
- Decomposed into three slices:
  [#282](https://github.com/PollyGlot/google-play-cli/issues/282) (`orders view`,
  single — walking skeleton),
  [#283](https://github.com/PollyGlot/google-play-cli/issues/283) (`orders view`,
  batch), and
  [#284](https://github.com/PollyGlot/google-play-cli/issues/284)
  (`orders refund`, gated). #283 and #284 are blocked by #282.
- The refund pattern (per-resource, `--confirm`, exit `3`, no bulk, opt-in
  escalation flag) is the template the next money-moving write inherits.
