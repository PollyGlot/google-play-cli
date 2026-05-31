# `metadata`: the per-locale store-front axis and its sync model

## Status

accepted

## Context

`gplay metadata` owns one axis of an app's Google Play presence: the
**Store front**, i.e. everything keyed **by locale** — Listings text
today (`edits.listings`: title, short/full description, promo video),
store images in future scope (`edits.images`: icon, feature graphic,
screenshots per locale and form factor). Its on-disk form is the
**metadata tree** (`metadata/<locale>/…`), reconciled with Play by
`metadata apply`.

The boundary is deliberate. App-**global** surfaces — default language
and contact details (`edits.details`), country availability
(`edits.countryavailability`) — are keyed by app, not locale, carry
scalar/global semantics, and belong with the `apps` namespace (gplay
already reads details via `apps info`). They never enter the metadata
tree. The test for any future store-presence surface is "is it keyed by
locale?": yes → `metadata`; no → `apps`.

This ADR records both that boundary and the reconciliation model
`metadata apply` uses — established now with Listings, but designed to
extend to images without re-litigation. One piece deliberately diverges
from `releases upload`, which is exactly the kind of surprise that needs
recording.

Unlike `releases upload`, which *creates* a new artifact on a track (and
can stage it as a draft for `production`), a committed Listing is **live
on the store immediately, for all users, with no track or draft to fall
back on**. That difference drives the safety model below.

## Decision

1. **Additive by default; `--prune` to delete.** `apply` upserts only
   the locales/fields present on disk. A locale live on Play but absent
   locally is left untouched and reported, never deleted by omission.
   Deletion (`edits.listings.deletegroup`) requires explicit `--prune`
   (+ `--confirm`), and `--prune` refuses to remove the app's
   `defaultLanguage` Listing (the API requires it to exist).

2. **Field file: missing ≠ empty.** Inside a locale present on disk, a
   *missing* field file leaves the online value untouched; a *present
   but empty* file clears the field (`""`). This requires PATCH
   semantics, not a full-resource PUT. (`title`/`fullDescription` are
   required non-empty by Play, so an empty file for those is a
   validation error, not a clear.)

3. **`--dry-run` is online — it diffs disk against Play.** It opens a
   read Edit, fetches the live Listings, prints the per-locale/per-field
   delta, and discards without committing. This **diverges** from
   `releases upload --dry-run`, which is offline (ADR-0002): `upload`
   *creates* (nothing to compare), whereas `apply` *reconciles* — a
   meaningful dry-run of a reconciliation must read the other side. The
   offline role (char limits, known locales, format) is held by a
   separate `gplay metadata validate`, which needs no credentials.

4. **Real writes require `--confirm`.** Because every committed Listing
   is live-production-facing, `apply` without `--confirm` refuses and
   points at `--dry-run` (exit 2). This reuses the existing meaning of
   `--confirm` in gplay ("yes, touch live production"; ADR-0002) rather
   than inventing a new gate. `CI=true` does **not** auto-confirm:
   `CI=true` governs output format (ADR-0005), never mutation, so a
   stray env var can't publish.

5. **One Edit, one commit, atomic.** All locales are patched inside a
   single Edit and committed once. On any per-locale failure the Edit
   auto-discards (existing `edits.WithEdit`), so the store is never left
   half-applied — and one commit per `apply` (not N) conserves Google's
   daily publish quota.

6. **`--dry-run --output json` is a gplay-defined diff schema** (an
   explicit ADR-0003 exception, like `apps info`): a flat
   `{"package", "changes":[{locale, field, op, …}], "summary":{…}}`
   where `op ∈ {create, update, clear, unchanged, untouchedLocale}`.
   Flat so a CI gate is one `jq` line (`.summary.create + .summary.update
   > 0`) and so future image scope adds records without reshaping. The
   real `apply` (a write) returns the upstream `listings.patch` bodies
   keyed by locale — also an ADR-0003 exception, because N locales can't
   be a single verbatim pass-through (same reasoning as `apps info`).

## Consequences

- The default `apply` invocation cannot lose data (additive) and cannot
  silently publish (needs `--confirm`).
- Two `--dry-run` flags behave differently across commands. This ADR is
  the canonical answer to "why does `metadata apply --dry-run` hit the
  network when `releases upload --dry-run` doesn't?" — it is intentional,
  not a bug.
- Items 1, 3, and 4 (flag names and their semantics) fall under the
  Public contract (ADR-0010): stable from GA, changeable only with a
  major bump. The diff JSON schema (item 6) is likewise contract once GA.
- ADR-0003's Exceptions list must gain two entries: `metadata apply
  --dry-run` (gplay diff schema) and `metadata apply` (per-locale
  `listings.patch` bodies, not a single pass-through).
- `apply` and `pull` are symmetric around the missing-vs-empty rule, so
  `pull` then `apply` with no edits is a guaranteed no-op.
