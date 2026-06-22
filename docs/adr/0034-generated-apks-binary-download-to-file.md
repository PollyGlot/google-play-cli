# Generated APKs surface: Edit-free reads + gplay's first binary download-to-file gesture (`generatedapks`)

Triaged from [#294](https://github.com/PollyGlot/google-play-cli/issues/294) and
grilled into PRD [#299](https://github.com/PollyGlot/google-play-cli/issues/299)
under [ADR-0026](0026-maximal-admin-api-coverage.md) (maximal admin-API
coverage). The `generatedapks` resource exposes the APKs Play generates and
signs from an uploaded AAB: list their download metadata, then download the raw
signed bytes. Two read-only endpoints — but `download` is the first command in
gplay that writes **opaque binary bytes to a local file**, so it needs its own
conventions.

The decisions:

- **Namespace: `releases generated list` / `releases generated download`.** A new
  grouping noun `generated` under `releases`, alongside `sharing` and
  `expansion-files` (the [ADR-0030](0030-android-publisher-long-tail-surfaces.md)
  sub-surfaces). These are post-upload artifacts of a release, so `releases` is
  the honest home — not a top-level namespace.

- **No Edit lifecycle.** Unlike `releases list` (which opens a read-only Edit),
  the `generatedapks` endpoints are **application-scoped**, not under
  `/edits/{editId}/`: `GET /applications/{packageName}/generatedApks/{versionCode}`
  and `.../downloads/{downloadId}:download`. The client issues a direct GET — it
  must **not** cargo-cult an Edit wrapper. Confirmed against
  `docs/discovery/paths.txt`.

- **`download` is a new domain verb** (admitted under the
  [ADR-0019](0019-canonical-verb-vocabulary.md) test). `view`/`list` read
  structured data onto stdout; `download` writes **opaque bytes to a file** — a
  gesture `set`/`create`/`view` cannot state honestly. It mirrors the API method
  name `.download`, the same way `add-targeting` mirrors `addTargeting`. Added to
  the DESIGN §0 domain-verb list; no verb-gate rename is involved.

- **Destination is `--dest`, not `--output`.** `download`'s payload is raw bytes,
  not a [Renderable](../../CONTEXT.md#renderer), so the command does **not** expose
  the global `--output json|table|markdown` flag (same rule as `auth login`,
  DESIGN §"Commands without `--output`"). The artifact is written to **`--dest
  PATH`** (required in v1); **`--dest -`** streams the bytes to stdout for piping.
  This deliberately avoids a confusing `--output` / `--output-file` collision.
  Bytes are streamed (`io.Copy` to an `io.Writer`), never buffered whole — a
  universal APK can be large. On success a stderr `✓` line names the byte count
  and destination (DESIGN §8); stdout stays the data path.

- **`list` is `list`, keyed by `--version-code`.** The API verb is `.list`
  returning a `GeneratedApksListResponse` (read-many). `--version-code N` is a
  required `Int` flag, consistent with `releases expansion-files view` (also keyed
  by `apkVersionCode`). `download` takes `downloadId` as its **positional** (the
  addressed artifact), mirroring `orders view <orderId>`. The grouped-by-signing-key
  envelope is flattened for the human views to one row per artifact (type · module
  · split/variant/slice id · downloadId · short cert hash); `--output json` stays
  the verbatim API body ([ADR-0003](0003-json-passthrough.md)).

- **Pure reads, no special permission.** Both commands require only that the
  service account is invited on the app — no financial capability (unlike
  `orders`). `403` → exit `11` (agent-resolvable refusal naming the missing app
  access); unknown version code / download ID (`404`) → exit `30`; 5xx → `40`;
  network → `50`. Neither command is `MarkMutating`; neither is gated by
  `GPLAY_READONLY` (ADR-0024).

- **Ships `[experimental]` first** ([ADR-0010](0010-versioning-public-contract-and-ga.md)),
  like every new surface under ADR-0026.

## Why

1. **The download-to-file convention is the irreversible part.** Listing
   generated APKs is unremarkable — another Edit-free read. But `download` sets
   the precedent for *every* future binary-fetch in gplay (a CSV report, an
   exported artifact). Pinning it now — `--dest` with `-` for stdout, streamed,
   `✓` on stderr, no `--output` — keeps the next binary surface from inventing a
   second, divergent idiom. Reusing `--output` for a file path, or buffering the
   whole artifact, would each have been a quiet long-term mistake.

2. **`download` earns its place rather than bending an existing verb.** The
   tempting shortcut is `pull` (it exists) or overloading `view`. But `pull` is
   the metadata-tree gesture (remote → local *editable text*), and `view` reads a
   resource as structured data. Fetching one opaque signed artifact to disk is
   neither; calling it `download` — the API's own name — is the honest label and
   keeps the verb vocabulary legible.

3. **Edit-free matters for correctness, not just simplicity.** An agent that
   pattern-matches on `releases list` would wrap these reads in an Edit, which the
   endpoints don't accept. Writing the no-Edit rule into the ADR (and the slice
   acceptance criteria) is cheaper than a debugging round later.

4. **Scoped to two endpoints, deliberately.** No batch download (the API has
   none), no filtering on `list` (the API returns the full set), no auto-derived
   destination filename in v1 (split APKs have no natural name — `--dest` is
   explicit). Each exclusion is a non-goal recorded so the surface stays a quick
   win, not a yak-shave.
