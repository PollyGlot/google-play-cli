# ADR-0057: Bounded responses and pagination

## Status

accepted (2026-11-20)

amended (2026-11-20): implementation decisions moved from code comments

## Context

gplay reads every response into memory before passing it through
(ADR-0003), and many listings span several pages. Both need a bound, and
neither bound may ever yield output that looks complete when it is not. The
user-facing rule for `--limit` is in DESIGN §9 ("A listing cut by `--limit`
always says so"); the sizing and walking rules lived in code comments, and
the comment purge (the `scripts/lint/comments.py` ratchet) gathers them here.

## Decision

### Body caps

- **A 2xx API body is capped at 4 MiB** (`api.MaxAPISuccessBodyRead`). A
  `tracks.update` answer for an app with many locales and release notes
  passes 64 KiB easily; 4 MiB leaves headroom for any sane Play response
  while bounding memory against a runaway server.
- **A body past the cap is an error, never truncated.** The read takes one
  byte more than the cap: reaching it proves the body was cut, and a cut JSON
  document must never reach `--output json` as if it were whole.
- **Media downloads have no cap.** They stream to a writer
  ([ADR-0034](./0034-generated-apks-binary-download-to-file.md)).
- **Cloud Storage reads have their own caps**: 4 MiB for a list page, 64 MiB
  for a fetched object. Monthly review CSVs stay well under 64 MB even for
  high-volume apps, and the cap stops a hostile or misconfigured bucket from
  exhausting memory. A caller that knows its objects better picks its own cap
  (`FetchObjectLimit`).

### Page sizes

- **A page size is chosen against the body cap.** Play pretty-prints by
  default (Discovery: `prettyPrint` defaults to true, and gplay leaves it on),
  roughly doubling the compact size. The monetization catalogs page at 20:
  a subscription with three base plans priced in every region weighs about
  140 KB, a one-time product with a buy and a rent option about 90 KB, so
  pages of 100 reached 13 MB and 9 MB, past the cap (#790). The body-cap tests
  render pages as Play sends them, pretty-printed and priced in every region,
  so a page size that outgrows the cap fails offline.
- **Otherwise a listing asks for the largest page the API allows**, to cut
  round trips (reviews at 100, ADR-0055).

### Auto-paginated walks

- **Whether a method is paginated is read from its response schema**: a
  `nextPageToken` (every current surface) or `tokenPagination` (the legacy
  in-app products list). A `pageToken` query parameter would miss the vitals
  `*.query` methods, which take their token in the POST body. The fact is
  declared per method in the registry
  ([ADR-0065](./0065-api-registry-and-discovery-snapshot.md)).
- **A walk follows the continuation token to the end** (`api.Paginate`), or to
  an explicit `--limit`, which it reports as truncation.
- **The Reporting API needs it most**: its default pages are small (10
  anomalies, 50 error issues), so a single page silently under-reports.
- **The envelope is rebuilt from the verbatim items of every page**; per-page
  `nextPageToken`, `pageInfo` and `tokenPagination` are walk state, not data
  (ADR-0003 implementation notes).
- **Truncation is a return value, not a stderr write.** The HTTP layer
  returns it and the command turns it into the warning (`rc.WarnTruncated`).
- **What counts as truncated**: the server still had pages when the limit was
  reached, or the last page overshot the limit and was cut. Hitting the limit
  exactly on the last page is exhaustive. Keying the signal on the token alone
  missed the second case, a page larger than the limit with no token left.
- **A present token is taken at its word.** The Reporting API documents the
  token as omitted once no page follows, so a token means more exists. Should
  a server ever hand back one for an empty final page, the cost is one
  needless `warning:` on stderr, never wrong data on stdout; the opposite
  mistake, silence over hidden results, is the one PRD #446 exists to prevent.
- **A repeated token fails the walk** with an error, rather than requesting
  the same page until the deadline.
- **A page bound (`MaxPages`) is an error when reached with a token still
  set**, never a silent stop: a partial list that looks whole is what the seam
  exists to prevent.
- **An empty listing yields nil items**; a caller that renders an empty array
  normalises it.
- **A reconciliation read never stops early.** The monetization catalogs walk
  to completion whatever the page size, since a missing page would read as
  deletes in a Reconciliation plan
  ([ADR-0041](./0041-declarative-monetization-catalog.md)).

## Consequences

- No gplay output is a silent prefix of the truth: either a cap error, a
  truncation warning, or the whole listing.
- A new paginated read uses `api.Paginate` and a page size checked against
  the cap with a realistic, pretty-printed fixture.
