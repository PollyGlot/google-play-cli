# Bulk reports: header-driven reads of the Reporting bucket (`gplay reports`)

## Status

accepted, extends [ADR-0037](./0037-reviews-history-gcs-csv-reports.md)

## Context

A gplay user who wants an app's install and uninstall numbers, Store listing
acquisition figures or revenue has no programmatic way to get them through a
JSON API: the Play Developer Reporting API carries vitals only, and the
Android Publisher API carries no statistics. The one channel is the set of
monthly **Bulk reports** Play deposits in the developer's
[Reporting bucket](../../GLOSSARY.md) (`gs://pubsite_prod_rev_<developerId>/`).
Without gplay, a CI job, an agent or a finance script falls back to `gsutil`,
hand transcoding from UTF-16, ZIP extraction and guesswork about the missing
permission.

[ADR-0037](./0037-reviews-history-gcs-csv-reports.md) taught gplay to read
that bucket for reviews and left statistics and sales to "their own future
decision". [PRD #693](https://github.com/PollyGlot/google-play-cli/issues/693)
is that decision; its spike (2026-10-12, Play Console Help "Download and
export monthly reports") records each family's object path, format, split and
permission. Three findings shape the design:

- **The header is the only reliable schema.** Columns changed several times
  (Store performance re-exported in March 2021, legacy acquisition ended in
  August 2021, new Fee Description values in October 2026), and Google adds
  columns without notice.
- **Encodings differ and are partly unconfirmed.** The statistics files are
  documented as UTF-16; the CSVs inside the finance ZIPs are not documented.
- **Permissions differ per family.** Statistics need "View app information"
  (global); finance needs "View financial data" (global).

## Decision

1. **Surface: a `gplay reports` group, `[experimental]` as a whole**
   ([ADR-0010](./0010-versioning-public-contract-and-ga.md),
   [ADR-0042](./0042-one-zero-ga-and-stability-label-mechanism.md)). The
   columns are Google's, so the output cannot be frozen. `reports list` is the
   inventory (family, package, month, dimension, size, updated); the
   dimension is filled for the per-package families only, since it is what
   `--dimension` takes (an earnings name suffix stays in the object name).
   Every other
   leaf reads one family and is named after it (`reports installs`, then
   `ratings`, `crashes`, `store-performance`, `subscription-stats`, `earnings`,
   `sales`): a documented exception to the verb grammar, of the same kind as
   the `vitals` presets ([ADR-0027](./0027-vitals-second-service-scope-readonly.md)),
   since there is no resource to `view`, only an export to read. One leaf per
   family, rather than one `reports get --family`, so each `--help` documents
   its own path, permission and output.

2. **Same channel as `reviews history`.** Raw HTTP to the Cloud Storage JSON
   API through `internal/play/gcs`, the `devstorage.read_only` scope minted per
   leaf, the bucket derived as `pubsite_prod_rev_<developerId>` from the
   developer-account axis ([ADR-0015](./0015-developer-account-addressing-rides-on-account.md))
   with `--bucket` and `--developer-id` overrides. The GCS client gains the
   size and update time of a listed object and a read cap chosen by the
   caller. The bucket plumbing (name, access error, month helpers) moves to a
   shared package that `reviews history` uses too; its contract and tests do
   not change.

3. **The family catalogue is data.** One deep module (`internal/reports`)
   declares each family once: object-key prefix, per package or account-wide,
   required permission, Console page, default split, read cap, table columns
   hidden by default, help text. It parses object names back into family,
   package, month and split, leniently (a package may hold underscores, the
   extension's case is ignored, the account-wide suffix is free-form), and
   picks the exports to read. Adding a family is one entry and one
   registration line.

4. **Header-driven output, a documented deviation from
   [ADR-0003](./0003-json-passthrough.md)**, the same class as ADR-0037: the
   upstream is a CSV file, there is no JSON response to mirror.
   `--output json` emits

   ```json
   {"report": {"family", "package", "product", "months", "split", "objects"},
    "columns": [{"key", "header"}],
    "rows": [{"<key>": "<value>"}]}
   ```

   - `product` appears for subscription statistics only, whose exports are
     per subscription product (`--product`).
   - **Key rule.** The words of a header are its runs of letters and digits;
     the first is lowercased, each next one gets an upper-case first letter and
     the rest lowercased: `Daily Device Installs` is `dailyDeviceInstalls`,
     `Daily ANRs` is `dailyAnrs`. A repeated key gets the first free numeric
     suffix from 2; a header with no word becomes `column<N>` (its 1-based
     position). The rule reproduces the keys ADR-0037 chose by hand.
   - **Values are verbatim strings**: never typed, never trimmed, so
     postcodes, product ids and version codes keep their leading zeros.
   - **Rows keep column order** and leave out a column they have no cell for.
     A cell past the header is kept under a positional key, not dropped.
   - **A month range merges by union of columns** in first-seen order, so a
     range across a Google column change neither fails nor misaligns.
   - The table shows every column but a per-family hidden list (a column
     constant across rows, rare counters), so a new column is visible;
     `--columns` takes any key.

5. **Encoding is detected, never assumed.** A BOM selects UTF-16 LE, UTF-16
   BE or UTF-8. Without one, a NUL in the first code unit means UTF-16 (a
   UTF-8 CSV never starts with a NUL), its side giving the byte order; anything
   else is UTF-8. UTF-16 with an odd number of bytes was cut mid character
   and is refused (exit 20), never decoded with its last byte dropped. The
   CSV is RFC 4180. The decoder is fuzzed.

6. **Selection is checked against the bucket, never against a frozen list.**
   Without `--month` a family reads the latest month present for the package.
   `--since/--until` read every month of the range. `--dimension` names the
   split file and is validated against the splits present for the months
   asked, so a dimension Google adds works the day it appears. Without it,
   `overview` is read when present; otherwise the command fails naming the
   dimensions present. A month of a range with no export is skipped with a
   stderr warning. An account-wide family (earnings, sales) has no
   dimension: every export of the month is read, in name order, whatever
   follows the month in its name (the earnings suffix is unconfirmed, and
   picking one file would drop the others). Its `--package` filters rows on
   the package column the header names (`Package Name`, `Package ID`, or
   `Product ID`, the finance reports' name for it), never the export; the
   Project pin does not apply, since it names the app a repository ships, not
   the rows to keep. Each export is filtered on its own header, as it is
   decoded, so a range across a rename of that column keeps every month.
   A header with no package column makes `--package` a usage error (exit 2);
   an export with no header at all is an empty report. When `--package`
   keeps none of the rows read, a stderr note says so (`0 of N rows matched
   --package X`), exit 0. The object's extension, not the family, picks the
   decoding: the account-wide name shape accepts `.csv`, read as is, and
   `.zip`, read under the bounds of §10. A name's month must be a calendar
   month (01 to 12): `_202613_` is no export.

7. **"No report" is exit 30 (absent resource)** naming the family, the
   package (for a per-package family) and the bucket, the code
   `reviews history` gives the same situation: the command line is
   well-formed, the export is not there (the fix is in `--package`,
   `--month`, `--bucket`, or in waiting for Play to publish). A bad
   `--dimension` or `--columns` is a usage error (exit 2). A 404 on an export
   the listing has just named is no access problem: Play republished it in
   between, so the message says to run again (still exit 30), and a range
   skips that month with a warning, like an unpublished one.

8. **A 403 names the permission of the family asked.** The access error
   names "View app information" (global) for statistics, "View financial
   data" (global) for finance, plus the Console's Download reports page whose
   "Copy Cloud Storage URI" button gives the real bucket for `--bucket`; it
   keeps the upstream exit code (403 is 11, 404 is 30). `reports list` makes
   a 403 on one family a stderr warning, so the statistics inventory still
   prints for a service account without the financial grant; when no family
   can be read it fails with that error.

9. **Buyer location is hidden by default (finance families).** Estimated
   sales carry `City of Buyer`, `State of Buyer`, `Postcode of Buyer` (or
   `Postal Code of Buyer`); earnings carry `Buyer State`, `Buyer Postcode`
   (or `Buyer Postal Code`). City, state and postcode
   are removed from the output unless `--include-buyer-location` is passed;
   the country stays visible (revenue per country is the common aggregate and
   locates no one). Masking is a pure filter on the header text,
   case-insensitive, on the header's words: "buyer" together with city,
   state, province, postcode (or "post code"), postal or zip, so it survives a
   Google rename. Words split on any non-alphanumeric character and at a
   camelCase boundary, and a word glued to "buyer" (`buyercity`,
   `cityofbuyer`) is judged on what remains. The country exempts a column
   only when no place is named: `Country of Buyer` stays, `Country / City of
   Buyer` is masked. Province and zip widen the documented spellings to the
   obvious renames, at no cost to the country aggregate. A stderr notice says how many columns were hidden and names
   the flag. CI logs and agent contexts thus never collect a buyer's location
   by accident.

10. **Bounds, per command.** The exports come from a bucket gplay does not
    control and a range may span ten years, so every bound fails with an
    error naming it, at exit 20 (client-side validation), never with a
    truncated report nor an exhausted memory. Following the artifact
    preflight of
    [ADR-0046](./0046-artifact-preflight-hand-rolled-manifest-readers.md),
    a finance object over 128 MiB is refused before it is read (on the size
    the bucket listing reports, then again by the read cap on the bytes
    fetched, for an object grown since the listing), and an archive with
    more than 16 members is refused. The member count is taken once
    archive/zip has parsed the central directory, which the object cap
    bounds: a 72 MiB archive of a million empty entries costs about 200 MiB
    of heap (measured), so no hand-rolled pre-parse is warranted. Only
    `.csv` members are read.

    What the exports decode to is bounded by **one budget for the whole
    command**, not per file or per archive: a per-archive budget let a
    twelve-month range hold twelve times its bound. Two quantities are
    bounded, because neither alone bounds the memory:

    - **512 MiB of CSV read**, every export together (the bytes of a plain
      export, the inflated bytes of an archive member). A member's declared
      size is checked against what is left before a byte is inflated, and
      the bytes actually read are charged as they come, since a declared
      size can lie.
    - **24 Mi cells held**, a row counting as its cells plus two. Bytes alone
      do not bound memory: a 20 MB archive of `A` then ten million `x` lines
      grew past 3.7 GB under the per-archive budget (about 400 bytes a row,
      one map each). Rows that `--package` drops are filtered as they are
      decoded and never held, so they cost bytes, not cells.

    The exports are decoded as they are read (UTF-16 transcoded on the fly,
    ZIP members streamed, no text held whole), a row is its CSV record plus
    a layout shared by its export rather than a map, and the JSON envelope
    streams to stdout instead of being marshalled, compacted and indented in
    memory (its bytes are unchanged). Measured on that representation, a
    cell costs about 28 bytes, its text included, on one-cell rows as on
    24-column earnings rows: hence the weight of two cells per row, and at
    the bound about 700 MB of rows held whatever their shape. Peak resident
    memory on `A` then N one-cell lines, before and after: 426 MB and 99 MB
    for one million lines, 1.65 GB and 388 MB for four million, 3.7 GB and a
    refusal at 1.1 GB for ten million. The cell bound reads about 950,000
    rows of a 24-column earnings report (about 185 MB of CSV) in one
    command; past it, a narrower `--since/--until` reads, which the error
    says.

## Considered options

- **Typed structs per family**, as `reviews history` has. Rejected: every
  column change would need a gplay release, and a column Google adds would be
  silently dropped.
- **Rows as JSON maps.** Rejected: Go sorts map keys, so the row order would
  stop matching the header. Rows are written in column order instead.
- **Typing the values** (numbers, dates). Rejected: it loses leading zeros and
  precision, and the column semantics are Google's to change.
- **A budget per archive, or bytes only.** Rejected: a range multiplied the
  first, and the second let a small file of short rows exhaust memory (§10).

## Consequences

- A column Google adds, renames or removes reaches users without a release;
  the JSON keys follow the header, so a rename is a key change users see.
- `reports` stays experimental until the families are exercised against real
  buckets (the `overview` dimension, the earnings suffix and the ZIP encoding
  are still unconfirmed).
- Out of scope, held by [#697](https://github.com/PollyGlot/google-play-cli/issues/697):
  invoice billing, Play balance KRW, the cancellation survey, the legacy
  acquisition exports and payouts. gplay never writes to the bucket nor caches
  reports, and does not aggregate beyond merging a month range.
