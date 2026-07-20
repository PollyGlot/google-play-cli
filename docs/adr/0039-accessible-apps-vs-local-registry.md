# Server-authoritative app discovery: `apps accessible list`, distinct from the local registry

## Status

accepted

## Context

[ADR-0026](./0026-maximal-admin-api-coverage.md) puts every Play **admin**
API in scope. `docs/COVERAGE.md` tracked
`playdeveloperreporting.apps.search` (1 method — "Searches for Apps
accessible by the user") as a discovery candidate: a **server-side**
enumeration of the Apps the calling credential can access, with
pagination. It rides the Play Developer **Reporting** service, whose
read-only scope gplay already opened for `vitals`
([ADR-0027](./0027-vitals-second-service-scope-readonly.md)).

Today `apps list` prints gplay's **local registry** — the packages an
operator has run `apps add` on. The Android Publisher API has no "list my
apps" endpoint, so the registry is a hand-maintained working set, not a
cache of anything. `apps.search` is the first server-authoritative answer
to "which Apps can this credential reach?" — which raises the question the
issue ([#347](https://github.com/PollyGlot/google-play-cli/issues/347))
was filed to settle: replace `apps list`, augment it, or add a new
surface?

The decisive realization (from the #347 grilling, 2026-07-17): **the local
registry and the `apps.search` result are two different sets, and they do
not coincide.** The two capabilities are backed by **different
permissions**:

- Seeing an App via `apps.search` requires **Reporting** access on it.
- `apps add`-ing an App requires **Android Publisher** (edits) access on
  it.

A service account can hold one grant without the other. So a credential
can be able to add an App it does **not** see in `apps.search`, and can
see hundreds of org Apps in `apps.search` that it does **not** drive and
has no registry entry for. The registry is a **chosen workspace**, not a
failed cache of the API.

## Decision

1. **A new surface, not a replacement or a mode flag.** Ship
   `apps.search` as **`apps accessible list`** — a new grouping noun
   (`accessible`) under the canonical read verb (`list`). `apps list`
   keeps printing the local registry, unchanged.

2. **`apps accessible list` is server-authoritative discovery.** It calls
   `apps.search` on the Reporting service and prints `{packageName,
   displayName}` per App; `--output json` passes the
   `SearchAccessibleAppsResponse` through verbatim
   ([ADR-0003](./0003-json-passthrough.md)), `nextPageToken` included. Its
   purpose is **bootstrap**: on a fresh install with an empty registry and
   unknown package names, discover the packages, then `apps add` the ones
   to work on. Its output feeds `apps add`; it is not a registry source
   and not a drift check.

3. **Caller-driven pagination.** `--page-size`/`--page-token`, one page per
   invocation, echoing `nextPageToken` verbatim — the `device-tiers` /
   `games` convention for recent list surfaces. **Not** the auto-pagination
   of DESIGN §5, which is specific to `reviews`.

4. **No new scope.** `apps.search` lives on the Reporting service whose
   read-only scope ADR-0027 already opened; the `list` verb is annotated
   with `token.ReportingScope` via `kernel.WithScope`. No new OAuth grant.

5. **New CONTEXT term "Accessible App"** records the registry-vs-server
   distinction so it survives the ticket.

## Consequences

- The registry stays the single source of truth for the working set and
  for per-Account config/aliases; `apps accessible list` is a read-only
  discovery layer beside it, never a writer of it.
- Any access-audit or drift-detection feature built on the **gap** between
  the registry and `apps.search` would report **structural false
  positives** (the two sets legitimately differ), so that class of feature
  is deliberately out of scope — permanently, not "later".
- The two surfaces can each answer honestly for its own question; a single
  command with a `--source` flag could not (it would have to pretend the
  two sets are interchangeable views of one thing).

## Alternatives rejected

1. **Replace `apps list` with `apps.search`.** Rejected: it would drop the
   local working set — the registry is chosen, not derivable from the
   server — and mislead on the permission split (a listed App may not be
   addable, an addable App may not be listed).
2. **`--source registry|server` flag on `apps list`.** Rejected: a command
   cannot honestly return either set depending on a flag when the two are
   different things, and [ADR-0019](./0019-canonical-verb-vocabulary.md)
   wants a new **noun** for a new resource, not a mode toggle.
3. **`apps discover` verb.** Rejected: `discover` is a synonym for `list`
   and fails the ADR-0019 canonical-verb admission test (verb-gate). The
   resource is new, so it gets a noun (`accessible`) under the existing
   `list` verb.
4. **Fold it into the `vitals` namespace** (it shares the Reporting
   service). Rejected: `apps.search` is about *identity/access*, not
   post-launch *metrics*; it belongs on the `apps` axis next to the
   registry it contrasts with, not among crash/ANR queries.
5. **Auto-paginate to a single merged list** (the `reviews` stance).
   Rejected: DESIGN §5 auto-pagination is `reviews`-specific; discovery
   over a potentially large org inventory uses the explicit
   `--page-size`/`--page-token` cursor like the other recent list
   surfaces.
