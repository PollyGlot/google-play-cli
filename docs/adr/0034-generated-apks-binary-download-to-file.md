# Generated APKs: Edit-free reads and the binary download-to-file convention (`generatedapks`)

## Status

accepted

## Context

[ADR-0026](./0026-maximal-admin-api-coverage.md) puts every Play **admin** API in
scope. PRD [#299](https://github.com/PollyGlot/google-play-cli/issues/299) wraps
the `generatedapks` resource — the APKs Play **generates and signs** from an
uploaded App Bundle, exposed by two read-only methods (`docs/discovery/paths.txt`):

- **`generatedapks.list`** — `GET …/applications/{packageName}/generatedApks/{versionCode}`
  → `GeneratedApksListResponse`: every split / standalone / universal APK, plus
  asset-pack slices and recovery modules, **grouped by signing key**, each
  carrying an opaque `downloadId`.
- **`generatedapks.download`** — `GET …/generatedApks/{versionCode}/downloads/{downloadId}:download`
  → the **raw signed APK bytes** (`alt=media`).

The CI/agent value is concrete: after `releases upload` of an AAB, fetch the
**exact APKs Play serves** to devices — to verify the signing identity, sideload
the served artifact, or archive it for reproducibility. It is read-only, moves
no money, and touches no Edit. Three decisions are cheap to get wrong and
expensive to rename once the companion skill (ADR-0021) pins to them.

## Decision

### 1. No Edit lifecycle — application-scoped reads

Unlike `releases list` (which opens a read-only Edit), the `generatedapks`
endpoints are **not** under `/edits/{editId}/` — they are addressed on the
**package axis** at `/applications/{packageName}/generatedApks/{versionCode}`.
The client (`internal/play/generatedapks/`) issues a direct GET and must **not**
cargo-cult `releases list`'s `edits.WithReadOnlyEdit`. This matches the
non-Edit precedent of Internal App Sharing and App Recovery
([ADR-0030](./0030-android-publisher-long-tail-surfaces.md)). Tests assert no
`/edits/` segment appears in the request URL.

### 2. `download` is a new domain verb (ADR-0019 admission test)

`view`/`list` read **structured data to stdout**; `download` writes **opaque
binary bytes to a local file** — a gesture `set`/`create`/`view` cannot state
honestly. It passes the [ADR-0019](./0019-canonical-verb-vocabulary.md) §2
domain-verb admission test and mirrors the API method name `.download` (the same
"name it what Google names it" precedent as `add-targeting` ↔ `addTargeting`).
It is added to the DESIGN §0 domain-verb list. New domain verbs are additive (no
rename, no verb-gate involvement), so admitting it breaks no skill or contract.

### 3. Destination is `--dest`, not `--output`

`download`'s payload is **raw bytes, not a Renderable**, so it does **not** expose
the global `--output json|table|markdown` flag — the same rule that exempts
`auth login` (DESIGN §"Commands without `--output`"). It writes to **`--dest PATH`**
(required in v1); **`--dest -`** streams to stdout for piping. A success line on
**stderr** names the byte count + destination with the `✓` marker (DESIGN §8);
stdout stays the pure data path (binary, or nothing for a file write). This
sidesteps a confusing `--output` (format) vs `--output-file` (path) collision.
The bytes are streamed (`io.Copy` to an `io.Writer`), never buffered whole, so a
large universal APK does not blow memory. A failed file transfer removes the
partial file rather than leave a corrupt APK behind.

### 4. `list` is `list`, addressed by `--version-code`; `download` takes the positional

`generatedapks.list` returns a `…ListResponse` (read-many) → the verb is `list`,
keyed by a required `--version-code N` `Int` flag (consistent with `releases
expansion-files view`, also keyed by `apkVersionCode`). On `download`, the
addressed artifact's `downloadId` is the **positional** (mirroring `orders view
<orderId>`); `--version-code` stays a required flag.

### 5. Table shape for `list`

The grouped-by-signing-key envelope flattens to **one row per artifact**: `type`
(split / standalone / universal / asset-slice / recovery) · `module` ·
`split/variant/slice id` · `downloadId` · short cert hash. Unprotected
split/standalone variants (present only under automatic protection) are included
— they carry downloadIds too. `--output json` stays the verbatim
`GeneratedApksListResponse` ([ADR-0003](./0003-json-passthrough.md)).

### 6. Exit codes / permissions

Pure reads requiring the service account to be invited on the app — **no**
financial permission (unlike `orders`). The standard taxonomy applies via
`api.Error`: `403` → `11` (agent-resolvable refusal naming the missing app
access), `404` (unknown version code / downloadId, or no generated APKs) → `30`,
`5xx` → `40`, transport → `50`. A local destination-file IO failure is
client-side → `20` (parity with `internal/play/sharing.LocalIOError`).

## Considered options

- **Open a read-only Edit for `list` (mirror `releases list`)** — rejected: the
  endpoint is not under `/edits/`; opening an Edit would be a wasted round-trip
  and a false mental model. (Decision 1.)
- **Reuse `view` for the download** — rejected: `view` promises structured data
  on stdout; binary-to-file is a different gesture. (Decision 2.)
- **Expose `--output` and add `--output-file`** — rejected: two `--output*` flags
  with different meanings (format vs path) is a legibility trap; `--dest` is
  unambiguous. (Decision 3.)
- **A single `download --all` that fetches every artifact** — out of scope for
  v1: the API has no batch download; one `downloadId` per call.

## Consequences

- gplay gains its **first binary download-to-file gesture**; `--dest` (with `-`
  for stdout) is the convention any future binary-fetch command reuses.
- `download` joins the DESIGN §0 domain-verb list; `generated` joins the
  `releases` sub-surfaces (next to `sharing` / `expansion-files`).
- CONTEXT.md gains **Generated APK** and **Download ID**.
- `generatedapks` flips to ✅ in `docs/COVERAGE.md` (both methods shipped).
- No filtering/pagination on `list` (the API returns the full set, no
  `pageToken`); auto-derived destination filenames are a possible later
  enhancement (split APKs have no natural name), so `--dest` is explicit in v1.
