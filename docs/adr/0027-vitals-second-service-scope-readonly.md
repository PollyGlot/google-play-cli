# ADR-0027 — Vitals: a second Google service, a second OAuth scope, read-only

Date: 2026-06-15
Status: accepted

## Context

[ADR-0026](0026-maximal-admin-api-coverage.md) puts the **Play Developer
Reporting API** (`playdeveloperreporting`) in scope: vitals (crash/ANR rates,
slow start/rendering, excessive wakeups, low-memory kills, stuck background
wakelocks), error reports/issues/counts, and anomalies. Grilling the vitals PRD
(#49) against the **live** Discovery document surfaced four facts the PRD got
wrong or omitted:

1. **It is a different service.** Not `androidvitals.googleapis.com` (that host
   404s) but `playdeveloperreporting.googleapis.com`, version `v1beta1`, with
   its own Discovery document.
2. **It needs a different OAuth scope.** Every gplay call today mints a token
   for `…/auth/androidpublisher` only; the Reporting API requires
   `…/auth/playdeveloperreporting`.
3. **It is not a CRUD API.** Each vital is a *metric set* queried by POST with
   metrics, dimensions, a timeline aggregation, and a server-reported freshness.
   There is no resource to GET — only metrics to query.
4. **Mappings are coupled in purpose, not in architecture.** ProGuard/R8
   deobfuscation files make crash stacks readable, but `edits.deobfuscationfiles`
   is an **Android Publisher** Edit resource (existing scope, Edit model), not a
   reporting surface.

## Decision

1. **Own service, own snapshot.** `playdeveloperreporting` v1beta1 is added to
   the multi-doc Discovery tooling (snapshot `playdeveloperreporting_v1beta1.json`),
   never conflated with the `androidpublisher` snapshot.
2. **Per-command OAuth scope.** The kernel gains a per-command scope annotation
   (analogous to `MarkMutating`, [ADR-0024](0024-readonly-environment-policy.md)).
   The default stays `androidpublisher`; `vitals` commands declare
   `playdeveloperreporting`. A vitals command mints a token for the reporting
   scope only — least privilege, no change to existing commands, and a
   missing-scope failure is localized to the `vitals` namespace. `auth doctor`
   gains a reporting-scope check.
3. **`vitals` is read-only.** The Reporting API only reads; nothing under
   `vitals` mutates Play state — which is also why the #238 `✓`
   success-confirmation convention (DESIGN §8) does not apply there.
4. **Mappings live under `releases`, not `vitals`.** `edits.deobfuscationfiles`
   is surfaced as `releases upload --mapping <file>` (the common AAB+mapping
   case) and `releases mappings upload <file> --version-code N` (after the
   fact). Folding a publisher Edit upload under a read-only reporting namespace
   would break namespace coherence, so mappings split out of the vitals PRD.
5. **Hybrid query model.** Opinionated presets (`vitals crashes`, `vitals anr`,
   …) sit over a generic `vitals query <metric-set>` that guarantees full
   coverage (ADR-0026). gplay never invents a metric or dimension — the
   supported set is read from the Discovery snapshot / schema index. This
   mirrors the project's recurring "curated convenience + raw always accepted"
   pattern (permission aliases, role bundles).

## Consequences

- A second OAuth scope enters the binary. Service accounts must be granted Play
  Console reporting access and have the Reporting API enabled in their project; a
  missing grant surfaces as a scoped auth error on `vitals` calls only, never
  affecting the publisher commands.
- JSON stays pass-through ([ADR-0003](0003-json-passthrough.md)): the raw query
  response is mirrored verbatim. `table` / `markdown` render the timeline (dates
  × metrics, grouped by the chosen dimension); the server-reported **freshness**
  is logged on stderr (like the reviews 7-day-window warning) so an empty window
  never reads as "zero crashes".
- The default opinionated window is `--since 28d` with `DAILY` aggregation (the
  Play Console default), `HOURLY` opt-in; the metric set's timezone is read from
  the API, never hardcoded.
- `vitals` ships `[experimental]` first
  ([ADR-0010](0010-versioning-public-contract-and-ga.md)), with its own companion
  skill ([ADR-0021](0021-companion-skills-repo.md)) when shipped.
- Side finding: the Reporting API's `apps.search` enumerates apps accessible to
  the service account — a lighter path to real app discovery than Cloud Resource
  Manager (tracked in `docs/BACKLOG.md`), to evaluate once vitals lands.
