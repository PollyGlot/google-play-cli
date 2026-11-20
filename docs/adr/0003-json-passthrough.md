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
- **Errors** are not API pass-through. The human line goes to stderr; under `--output json` a gplay-defined envelope (`{"error":{"code","exitCode","retryable","message",...}}`) also goes to stdout, carrying the upstream `reasons` verbatim ([ADR-0023](./0023-json-error-envelope.md), [ADR-0044](./0044-structured-diagnostic-codes.md)).

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
- **Reading a body follows proto3 JSON too.** A JSON `null` maps to the
  field's default, which is what an unset field is, so it counts as absent; an
  unset integer may be written as `0` or left out, so only a non-zero value is
  "set"; and any number may arrive as a string (`"0.8"` reads as 0.8). Code
  that inspects a request or response body (the request-constraint table of
  [ADR-0066](./0066-offline-test-harness.md)) applies these rules rather than
  Go's.
- **A field whose zero is meaningful is a pointer.** `omitempty` drops a zero
  `int` but keeps a non-nil pointer to 0, so an explicit `--point-value 0` or
  `--steps-to-unlock 0` reaches the wire while an unset field stays absent.
  gplay-authored shapes follow the same rule: a diff record's
  `liveChars`/`localChars` are pointers, so a create's `liveChars: 0` and a
  clear's `localChars: 0` are emitted while a locale-level record omits both.
- **HTML escaping is off on every JSON a command emits** (the encoder gate
  covers `commands/` and `cmd/`; the global config, the Project config and
  the edit pin are written with `json.MarshalIndent` and still escape). Go's encoder rewrites
  `<`, `>` and `&` as `\u003c`, `\u003e` and `\u0026` by default, which only
  helps JSON inlined in an HTML page. Left on, it made passthrough strings
  (review text, listing copy, a raw message re-indented on the way out) differ
  byte for byte from what the API returned, and turned `--package <pkg>` in an
  error envelope into text neither grep nor a human reads.
- **One encoder writes command JSON**: `output.WriteJSON` (indented, trailing
  newline) for what reaches stdout, and `output.Marshal` for compact JSON
  assembled from API bytes before it gets there (a merged page envelope, a
  composite view, a request body a `--dry-run` previews). `json.Marshal`
  escapes even inside a `json.RawMessage`, and `WriteJSON` cannot undo an
  escape already baked into the bytes. A test enforces it: under `commands/`
  and `cmd/`, any `json.NewEncoder`, `json.Marshal` or `json.MarshalIndent`
  fails (#588, #622), one rule with no triage; elsewhere, a function that
  builds an encoder must also turn HTML escaping off, so the escaped form
  cannot come back through a file gplay writes (the Discovery and Schema index
  writers).
- **Passthrough output is asserted byte-equal with the fixture body**, since
  the API owns that shape. Goldens are for gplay-authored shapes (views,
  dry-run previews, summaries: the frozen contract of
  [ADR-0010](./0010-versioning-public-contract-and-ga.md)), and the golden
  helper also checks that the output is one valid JSON document with no HTML
  escape, so a golden cannot freeze a broken encoder.
- **A wire golden recorded before a refactor is a pin.** A test that pins every
  request a package sends, byte for byte, against a golden recorded before the
  move onto the shared executor (#586), is never regenerated to make a wire
  change pass: a diff there is the regression the golden exists to catch.
