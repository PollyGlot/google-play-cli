# `--output json` is a pass-through of the Google Play Developer API response

## Status

accepted; amended (2026-11-20): implementation decisions moved from code comments

## Decision

For every command that wraps a Google Play Developer API call, `--output json` returns the API response **verbatim**, without renaming, flattening, or reshaping fields. The envelope key therefore varies per command (`{"reviews":[...]}`, `{"tracks":[...]}`, `{"bundles":[...]}`, etc.) because that is how Google's API natively shapes its responses.

Three reasons:

1. **Single source of truth for the schema.** Users and agents who want to know what a field means go to <https://developers.google.com/android-publisher/api-ref/rest>. gplay does not maintain its own JSON schema documentation.
2. **No mapping bugs.** Every rename or flattening is a place divergence with the API can sneak in. Pass-through has zero such risk.
3. **Forward-compatible by default.** When Google adds a field to a resource, it surfaces in `gplay ... --output json` immediately, without a gplay release.

The `table` output (TTY default) is **not** pass-through — we pick the columns that matter for a human reader. The `markdown` output follows the same rule as `table`. Only `json` is committed to API fidelity.

## Exceptions

- **`apps list`** has no API source (there is no `apps.list` endpoint — see the local-registry workaround). Its JSON shape is `{"apps":[...]}`, chosen to mirror Google's naming convention for consistency with the rest of the output.
- **`apps info`** (shipped as `apps view`) combines endpoints inside one read-only Edit (`edits.details.get` + `edits.listings.get` on the default language, plus an optional `edits.images.list` on the default language's icon slot). A single pass-through is therefore impossible. The JSON shape is `{"details":{...},"listing":{...},"icon"?:{"url":..,"sha256":..}}`, where each sub-object is the upstream body verbatim — so the three reasons above still hold inside the envelope (one source of truth per sub-object, no mapping bugs, forward-compatible). The `[experimental]` `icon` key is **omitted entirely** when the icon slot is empty (missing == empty, [ADR-0013](./0013-image-slot-reconciliation.md)); its `url` is an ephemeral preview link callers must not persist, `sha256` the durable handle ([ADR-0038](./0038-app-icon-faithful-read-no-cache.md)).
- **`metadata apply --dry-run`** emits a gplay-defined diff schema, not an API body — there is no upstream "diff" endpoint to pass through. Shape: `{"package","changes":[{locale,field,op,…}],"summary":{…}}` (see [ADR-0011](./0011-metadata-apply-sync-model.md)).
- **`metadata apply`** (the real write) touches N locales, so its result is the per-locale write bodies keyed by locale (`{"<locale>": <body verbatim>, …}`: `edits.listings.patch` for a live locale, `edits.listings.update` for one it creates), not a single pass-through — same reasoning as `apps info`. Each value is the upstream body verbatim, so the three guarantees hold per locale. (`metadata list` stays a plain pass-through of `edits.listings.list`.)
- **`subscriptions apply`** (with or without `--dry-run`) emits the gplay-defined Reconciliation-plan schema — there is no upstream "diff" endpoint, and an executed plan spans N create/patch/delete calls. Shape: `{"package","dryRun","changes":[{op,productId,fields?}],"summary":{…},"requires"?:[…]}`, flat so a CI gate is one `jq` line (see [ADR-0041](./0041-declarative-monetization-catalog.md)). (`subscriptions pull` stays API-shaped: the merged `ListSubscriptionsResponse` envelope, pages folded, the consumed `nextPageToken` dropped — the `team users list` precedent.)
- **Errors** are not API pass-through. They use a gplay-defined shape on stderr: `{"error":{"code":"<symbolic>","message":"<human>","details":{...}}}`. Wrapping the API error inside `details` preserves the upstream payload.

## Considered Options

- **Normalize to a gplay-specific shape** (e.g. `{"data":[...],"pageInfo":{...}}` envelope, snake_case fields, etc.) — rejected. We would own a schema we have no reason to invent, and every Google API evolution would require a gplay change.
- **Filter "noise" fields like `kind` and `etag`** — rejected for v1. The filter list becomes its own maintenance burden, and these fields are cheap to ignore client-side with `jq`.

## Implementation notes

- **Typed structs serve the human views only**, and stay as narrow as those
  views. The complete resource reaches the caller through the verbatim body,
  so a typed struct never grows to be exhaustive.
- **An auto-paginated read keeps every item verbatim** (a raw message per
  item) and rebuilds the envelope from them (`{"reviews":[...]}`,
  `{"voidedPurchases":[...]}`). Per-page `nextPageToken`, `pageInfo` and
  `tokenPagination` are walk state, not data, and are dropped. Client-side
  filters (`--stars`) and `--limit` select items without re-encoding them.
- **64-bit integers stay strings.** Google encodes `int64` and `uint64` as JSON
  strings (proto3 JSON: `scoreMin`, a Cloud Storage object `size`, a Money
  `units`), so gplay types them as strings and round-trips them verbatim;
  an integer type would break decoding.
- **A field whose zero is meaningful is a pointer.** `omitempty` drops a zero
  `int` but keeps a non-nil pointer to 0, so an explicit `--point-value 0` or
  `--steps-to-unlock 0` reaches the wire while an unset field stays absent.
  gplay-authored shapes follow the same rule: a diff record's
  `liveChars`/`localChars` are pointers, so a create's `liveChars: 0` and a
  clear's `localChars: 0` are emitted while a locale-level record omits both.
- **A wire golden recorded before a refactor is a pin.** A test that pins every
  request a package sends, byte for byte, against a golden recorded before the
  move onto the shared executor (#586), is never regenerated to make a wire
  change pass: a diff there is the regression the golden exists to catch.
