# Server-side commerce flows

gplay does not wrap `orders.reviewrefund` or the three
`externaltransactions.*` methods (create, get, refund). They are listed as
excluded (⚫️) in [docs/COVERAGE.md](../docs/COVERAGE.md).

## Why this is out of scope

These are runtime calls, not admin operations: the exclusion that
[ADR-0026](../docs/adr/0026-maximal-admin-api-coverage.md) reserves for
server-to-server flows, alongside the purchase-token methods.

- `orders.reviewrefund` answers a refund review. Its input is a token that
  reaches the app's backend through a Real-time Developer Notification; the
  answer is due when the event arrives, from the service that consumes the
  Pub/Sub topic. A human at a terminal has neither the token nor the timing.
- `externaltransactions.*` is how an app on alternative billing reports each
  transaction (and each refund) it processed outside Play. The backend that
  charged the user reports it, transaction by transaction, as it happens. A
  CLI invocation per transaction is the wrong shape for that duty, and a
  reporting gap is a compliance problem, not a convenience one.

Both flows belong in the app's backend, through a Google API client library,
next to the code that already holds the tokens and transaction ids.

## Prior requests

- #352: "orders.reviewrefund — new admin method (chargeback refund review);
  grill slice-vs-park"
- #295: "Parking: monetization — external transactions API (alternative
  billing / DMA reporting)"
