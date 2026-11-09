# Remaining bulk-report families

`gplay reports` reads statistics (installs, ratings, crashes, store
performance, subscriptions) and finances (earnings, estimated sales) from the
Play reporting bucket
([ADR-0050](../docs/adr/0050-bulk-reports-header-driven-reporting-bucket.md)).
It does not read the other families in that bucket:

- invoice billing (`invoice_billing_reports/`);
- Korea Play balance funding (`play_balance_krw/`);
- the cancellation survey free text (`subscriptions/cancellations/`);
- payouts (no path documented by Google);
- legacy acquisition exports (`acquisition/`, frozen since August 2021).

## Why this is out of scope

Each family is one catalogue entry on the existing foundation, but none can be
verified without a real file: they serve narrow programs (invoice billing,
Korean balance funding), have no documented path (payouts), are frozen
(acquisition) or carry free text written by end users that needs its own
privacy review (cancellation survey). Shipping a reader for a format nobody
has handed us is shipping a guess. No gplay user has asked for any of them.

A user who needs one family, and can confirm its file layout, reopens the
discussion for that family alone.

## Related limit

One `reports` command caps decoded CSV at 512 MiB and 24 Mi cells (ADR-0050
§10). If a high-volume account hits it on a single month, the fix is a
streaming row path (decode, filter, encode without holding rows), not a
bigger cap.

## Prior requests

- #697: "Parking: remaining bulk-report families"
