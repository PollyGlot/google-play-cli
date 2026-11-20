# ADR-0027 — Vitals: a second Google service, a second OAuth scope, read-only

Date: 2026-06-15
Status: accepted; amended (2026-11-20): implementation decisions moved from code comments

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

## Implementation notes

### The metric catalog is read from Discovery prose

Discovery carries the vitals catalog in prose, not in enums: the supported
metrics and dimensions of a metric set are bullet lists in the description of
its `:query` request's `metrics` and `dimensions` properties. gplay parses
that prose from the embedded Schema index, so validation is offline and no
list is kept by hand.

- **Only a bullet's leading backticked token is an entry.** A name mentioned
  inside a description ("rolling average of `crashRate`") is not mistaken
  for a catalog entry.
- **Aggregation periods vary per set** (the memory sets are `DAILY` only
  where `crashrate` also takes `HOURLY`), so they are read from the
  "Supported aggregation periods" prose of the request's `timelineSpec`,
  where Google writes them bare, in SCREAMING_SNAKE_CASE, not backticked. The
  `TimelineSpec.aggregationPeriod` enum is the fallback when that prose is
  missing. An unsupported `--period` is refused offline instead of by a
  server 400 (#440).
- **User cohorts are the shared `userCohort` enum filtered by each set's
  prose**: `crashrate` lists `OS_PUBLIC`, `APP_TESTERS` and `OS_BETA`, while
  `slowstartrate` says "The only supported value is `OS_PUBLIC`".
- **A cohort's prose can replace the set's dimension catalog** ("Only the
  following dimensions are supported:", `OS_BETA` today, which adds
  `osBuild`); otherwise the set's own dimensions apply.

### Error counts

- **`errors.counts` is a queryable metric set kept out of the metric-set
  registry.** It takes the same query and validation machinery as the rate
  sets, but it is surfaced as `vitals errors counts`, under the `errors`
  resource group, not through `vitals query`.

### Presets and help

- **The memory percentile sets have no preset.** They are niche and have no
  single headline metric worth a friendlier flag surface, so `vitals query
  <set>` is their only form. Every other metric set has a preset, and a test
  fails on a set left without one unless it is on that exemption list.
- **The `--period` help strings are written out**, not built from the
  embedded index: parsing the index cost every command about 2 ms at start,
  for a help string. A test fails when a help names a period the set refuses,
  or leaves out one it accepts, against the same supported-period set the
  command validates with, so an advertised period never ends in exit 2.

### Freshness and empty results

- **`vitals anomalies` always notes the reporting delay on stderr.**
  Anomalies are exceptional, so an empty result is the common, healthy case;
  the note keeps it from reading as "no anomalies, ever" when a very recent
  spike may not be reported yet. The metric-set queries do the same with
  their freshness line (Consequences above), naming the freshest datapoint of
  the window when there is one.
