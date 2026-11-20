# `metadata`: the per-locale store-front axis and its sync model

## Status

accepted

amended (2026-11-20): implementation decisions moved from code comments; corrected the delete endpoint name (decision 1) and the exit code of a missing `--confirm` (decision 4)

## Context

`gplay metadata` owns one axis of an app's Google Play presence: the
**Store front**, i.e. everything keyed **by locale** — Listings text
today (`edits.listings`: title, short/full description, promo video),
store images in future scope (`edits.images`: icon, feature graphic,
screenshots per locale and form factor). Its on-disk form is the
**metadata tree** (`metadata/<locale>/…`), reconciled with Play by
`metadata apply`.

The boundary is deliberate. App-**global** surfaces — default language
and contact details (`edits.details`) — are keyed by app, not locale,
carry scalar/global semantics, and belong with the `apps` namespace
(gplay already reads details via `apps info`). They never enter the
metadata tree. The original test here was binary ("is it keyed by
locale?": yes → `metadata`; no → `apps`). ADR-0012 widens it to a
three-axis test after finding that `edits.countryavailability` is keyed
by **track** and read-only: keyed by locale → `metadata`; by app →
`apps`; by track → `tracks`.

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
   Deletion (`edits.listings.delete`, which drops one locale's whole
   Listing; there is no `deletegroup` endpoint) requires explicit `--prune`
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
   points at `--dry-run` (exit 3, the safety-flag code of
   [ADR-0017](./0017-write-safety-and-agent-resolvable-refusals.md); this
   ADR first said exit 2, which predates that code). This reuses the existing meaning of
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

## Note (2026-09-22, #561)

Decision 2 assumed PATCH could create a locale. It cannot: on a language
with no Listing, `edits.listings.patch` answers 404, so every `create` the
diff planned for a new locale failed and discarded the Edit. `apply` now
picks the verb per locale from the `listings.list` read it already makes:
a locale live on Play keeps `listings.patch` (missing ≠ empty, unchanged),
a locale absent online is created with `listings.update` (PUT), which loses
nothing because there is no live field to preserve. The choice is per
locale, not per field `op`: a field-level `create` on a live locale (a
video added to `en-US`) stays a PATCH. A fallback to PUT on a PATCH 404 was
rejected: it spends a failing call per new locale, and a 404 there cannot
be told apart from a vanished Edit without parsing Google's message. The
real `apply` JSON (decision 6) is thus the per-locale write body, from
`listings.update` for a created locale.

## Implementation notes

### The Metadata tree on disk

- **The layout is `fastlane supply`'s, minus two parts.** One snake_case
  `.txt` per Listing field under a per-locale directory, with file names
  byte-identical to supply's, so an existing supply tree works as is. The
  `android/` path segment is dropped, and so is `changelogs/`: release notes
  belong to `releases`, not to the Store front.
- **The field table is a contract.** Each managed field's file name, API key,
  character limit and required-ness live in one ordered table (title, short
  description, full description, video), which is also the display order of
  `metadata list`, of a locale's diff records and of `validate`'s report.
  Changing a row is a deliberate change of the on-disk contract, and a test
  pins it.
- **Only the first level is read.** Each first-level directory is a candidate
  locale and each recognized field file in it a managed field. Everything
  else is ignored, which draws the metadata/releases boundary on disk: a
  top-level file, a non-locale directory such as `changelogs/`, an
  unrecognized file inside a locale, and any nested directory (so the
  `images/` subtree of [ADR-0013](./0013-image-slot-reconciliation.md) lives
  beside the `.txt` files without either codec reading the other's). A locale
  directory with no recognized field yields no entry rather than an empty
  Listing.
- **A misnamed field file is an error, not a skip.** A directory named like a
  known Play locale that holds a `.txt` file but no recognized field
  (`full-description.txt` for `full_description.txt`) fails the read. Skipped,
  the locale would vanish from the tree and `apply --prune` would delete its
  live Listing. The check keys on the embedded locale registry, so a typo
  inside a locale Google added after the release (one passed through
  `--allow-locale`) is still skipped.
- **One trailing newline, and nothing else, is the codec's.** Write appends a
  single `\n` to a non-empty value (a POSIX text file, line-oriented diffs)
  and Read strips exactly one. Stripping `\r\n` would read `x\r` back as `x`
  and break the `pull` then `apply` no-op for Play text ending in a carriage
  return, so the codec does not normalize CRLF: a file saved with CRLF keeps
  its `\r` in the value. Stripping one byte rather than trimming keeps a
  description that ends in a blank line.
- **Write never deletes.** It writes the managed fields it is given, a
  managed empty value as a 0-byte file; removing a locale or a field is
  `apply --prune`'s job, never a side effect of `pull`.

### Offline checks

- **The character limits are Google's published ones** (title 30, short
  description 80, full description 4000, from the Play Console Help page
  <https://support.google.com/googleplay/android-developer/answer/9866151>);
  no API exposes them. They count Unicode code
  points, not bytes, as Play does, and one function measures for both the
  validator and the diff's `liveChars`/`localChars`, so the two never drift.
- **The locale registry is a point-in-time copy** of the Play Console Help
  page "Supported languages and locales"
  (<https://support.google.com/googleplay/android-developer/answer/9844778>),
  matched case-sensitively since Play
  spells `en-US`, never `en-us`. Its job is catching typos and invented codes
  (`en_US`, `pt-XX`), not completeness: when in doubt a code is included, and
  a locale Google adds before gplay ships an update passes with
  `--allow-locale`.
- **`validate` is stricter than `apply`.** It answers "is each locale in this
  tree publishable on its own?": every locale present must carry a non-empty
  title and full description. `apply` is additive, so a locale that omits
  `title.txt` only leaves the online title alone; a tree `apply` accepts can
  still fail `validate`.
- **`apply` reuses `validate` minus that one rule.** A required field that is
  missing is dropped from the lint (the online value stays); a character
  limit, an unknown locale and a required field cleared to empty still block
  (exit 20), in dry-run and real mode alike, before any request.
- **A locale created incomplete is a warning.** The Discovery `Listing` schema
  marks no field required, so a new locale whose tree lacks a required field
  is still written, and the command names it on stderr.

### Prune guardrails

- **`--prune` against an empty tree is refused before any request**, dry-run
  included. With no locale on disk every online locale but the default
  language would be deleted: the signature of a mistyped `--dir` that still
  resolves to an empty or README-only directory, never an intent.
- **Both prune guardrails exit 2, not 3.** That refusal and the one protecting
  the default language's Listing cannot be lifted by a flag: the fix is
  another `--dir` or no `--prune`, so they are misuse, not a missing
  acknowledgment (DESIGN §9).

### The diff and the write

- **Unchanged fields are counted, not listed.** `summary.unchanged` carries
  them, `changes` does not, so the output stays bounded by the real delta for
  an app with many synced locales.
- **An online locale whose fields are all empty is still a key** in the
  online tree: the locale exists on Play, and prune and `untouchedLocale`
  detection key on its presence.
- **A failed per-locale write names its locale.** Inside the open Edit the
  package is known, so a 404 on one locale's write means that language has no
  Listing, not that the package is unknown, and the message says so. The
  exit code and the JSON error envelope stay those of the API error.
