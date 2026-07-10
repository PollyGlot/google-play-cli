# App icon retrieval: a faithful live read + content identity, no cache in gplay

Status: **Accepted** (2026-07-10). Motivated by a downstream consumer (a desktop
cockpit that lists a portfolio of apps and wants to render each app's real
store icon, falling back to an initial when absent). Tracked by PRD
[#337](https://github.com/PollyGlot/google-play-cli/issues/337) and its slices
([#338](https://github.com/PollyGlot/google-play-cli/issues/338),
[#339](https://github.com/PollyGlot/google-play-cli/issues/339),
[#340](https://github.com/PollyGlot/google-play-cli/issues/340)). The point of
the ADR is not the plumbing — it mostly exists already — but the **boundary**:
what gplay owns, and what the consumer owns.

## What already exists

The Play Store listing icon is already reachable today, end to end:

- `internal/play/images/images.go` — the `Image{ID, URL, Sha1, Sha256}` type
  (JSON tags verbatim, [ADR-0003](0003-json-passthrough.md)), the `Icon =
  "icon"` `AppImageType`, and `images.List(ctx, hc, pkg, editID, lang, type)`.
- `internal/play/edits/edits.go` — `WithReadOnlyEdit` (open → fn → **always
  discard**, never commit), which already dissolves the "you must open an Edit
  to read a listing image" friction.
- `commands/metadata/images/pull` — already downloads image bytes to disk
  (`download` + `imagetree.Write`), following the binary-download conventions of
  [ADR-0034](0034-generated-apks-binary-download-to-file.md).

So "can gplay get the icon?" is already **yes**. The open questions are
ergonomics (the icon is buried in an all-slots sweep) and, more importantly,
**where caching lives**.

## The decisions

- **No cache in gplay. This is the load-bearing decision.** gplay is a
  **faithful, live projection** of the API — every command hits the live
  service (`gplay apps view` "still hits the live API", DESIGN §"apps view").
  Introducing an on-disk icon cache would make gplay **stateful and potentially
  stale**, which breaks that contract. It is worse for the agent surface
  ([ADR-0029](0029-agent-discovery-surface.md)): an AI agent treats command
  output as **ground truth**, so a silently-cached, stale icon is a correctness
  bug, and it makes agent runs non-deterministic. The absence of any cache in
  the codebase today is a stance, not an omission — this ADR ratifies it.

- **Caching is a consumer responsibility, and gplay already ships the seam for
  it.** The real cost is downstream: an Edit-per-app fan-out when a consumer
  renders N icons at once (an Edit is `packageName`-scoped, so it cannot be
  batched across apps). That is a **policy** problem — refresh cadence, bitmap
  storage — and policy belongs to the consumer, not the CLI. gplay's
  contribution is **content identity**: it returns `sha256` on every `Image`
  (verbatim, ADR-0003). A consumer caches the decoded bitmap keyed by `sha256`
  and re-reads only when it chooses to. gplay stays pure; the consumer owns
  "how often" and "keep the bytes where."

- **The durable handle is `sha256` (+ a downloaded file), never `Image.url`.**
  The API documents `Image.url` as "a URL that will serve a **preview** of the
  image"; its resolution, authentication, and **expiry are undocumented**.
  Treat it as volatile: consumers that need the bytes should `download` them and
  key their cache on `sha256`, not persist the URL. gplay surfaces both, and
  says plainly which one is durable.

- **A focused read, not the 9-slot sweep.** `metadata images list` reads every
  `(locale × imageType)` slot to summarize a listing — the wrong tool for "give
  me just the icon." Add a `--type <AppImageType>` filter to `metadata images
  list` so a consumer can read a **single** slot (one locale, one type) in one
  read-only Edit. `--output json` stays the verbatim API body.

- **Icon rides `apps view` (the app-facing projection).** `apps view` already
  opens a `WithReadOnlyEdit`, resolves the default language, and returns
  `{defaultLanguage, title, contactEmail}`. Enrich its JSON with an optional
  `icon` object (`{url, sha256}`) resolved by `images.List(..., defaultLanguage,
  images.Icon)` **inside the same Edit** — near-zero marginal cost, and it is the
  natural accessor for a portfolio consumer that already calls `apps view` for
  app identity. Absent icon → field omitted (the consumer's initial-fallback is
  the honest default).

- **Bytes stay on the existing `download` gesture.** Materializing the icon
  bytes is `metadata images pull`, already governed by ADR-0034 (`--dest PATH`,
  streamed, `--dest -` to stdout, stderr `✓`). No new download path, no
  `--output` collision. We do **not** add byte output to `apps view`.

- **Scope reality, documented, not "fixed."** Reading a listing image rides a
  read-only Edit, and the listings API has **no `androidpublisher.readonly`
  scope** — so even a purely-read icon fetch requires the full
  `androidpublisher` scope. This is inherent to the API; gplay cannot lever it
  away. It is **not** gated by `GPLAY_READONLY` ([ADR-0024](0024-readonly-environment-policy.md))
  because it never mutates. Document it so a consumer with a "read-only" posture
  knows a write-scoped token is still required to render an icon. Refusals keep
  the house exit codes ([ADR-0017](0017-write-safety-and-agent-resolvable-refusals.md)):
  `403` → `11`, `404` → `30`, `5xx` → `40`, network → `50`.

- **Ships `[experimental]` first** ([ADR-0010](0010-versioning-public-contract-and-ga.md)),
  so the `icon` field shape on `apps view` and the `--type` filter can settle
  before they enter the public contract.

## Consequences

- gplay gains an ergonomic, faithful path to a single app's icon (`apps view`
  → `icon`, and `metadata images list --type icon`) without ever caching.
- The Edit-per-app portfolio cost is acknowledged and **pushed to the consumer**,
  with `sha256` as the documented caching key.
- The vision stays intact: gplay is still "the Google Play API, rendered as a
  CLI and for AI agents" — a live projection plus content identity, with state
  and policy left to whoever consumes it.

## Alternatives rejected

- **An in-gplay icon cache (TTL + cache dir).** Rejected: makes the CLI stateful
  and its output potentially stale; hostile to the agent-as-ground-truth
  contract; a large philosophical shift for a problem that is the consumer's.
- **A brand-new `apps icon` command.** Rejected as surface bloat: it would
  duplicate what `apps view` (+ `metadata images pull`) already express. If a
  dedicated intent verb proves warranted later, it can be added under the verb
  gate ([ADR-0019](0019-canonical-verb-vocabulary.md)); it is not needed now.
- **Returning `Image.url` as the primary handle.** Rejected: undocumented
  expiry/auth; `sha256` + downloaded bytes is the durable contract.
