# `compliance` namespace and the write-only Data Safety surface

## Status

accepted

## Context

`applications.dataSafety` lets an app declare what user data it collects,
how it is shared, and its security practices. It is a **publication
gate**: Google blocks releases when the declaration is stale, so it bears
on every app, not only monetised ones.

The original PRD (#114) assumed it would mirror `edits.listings`: a
`pull` / `validate` / `apply` triad following the **Additive sync** model
of [ADR-0011](./0011-metadata-apply-sync-model.md), filed under
`metadata`. Verifying the REST reference proved that assumption wrong on
four counts, and the four are API facts, not preferences:

1. **Write-only.** The resource exposes a single method,
   `POST /androidpublisher/v3/applications/{packageName}/dataSafety`.
   There is **no `get`** — the live declaration cannot be read back.
2. **Outside the Edits model.** It is a direct POST on the application,
   not an `edits.*` resource; it does not join an edit transaction.
3. **Keyed by app**, not by locale — so the store-presence axis test
   (locale→`metadata`, app→`apps`, track→`tracks`) of ADR-0011/0012 would
   file it under `apps`.
4. **One opaque CSV.** The body is `{ "safetyLabels": "<CSV contents>" }`
   — the same import/export CSV the Play Console uses.

## Decision

1. **A new `compliance` namespace**, not `apps` or `metadata`. The axis
   test is scoped to *store-presence* surfaces and does not bind a
   **regulatory** one. Data Safety, content rating, and other "App
   content" declarations form a cohesive family — app-keyed, write-heavy,
   outside Edits, and publication-gating — that no `apps`/`metadata`/
   `tracks` surface shares. Folding it into `apps` (identity + editable
   app-global config) would blur that namespace's meaning. Surface:
   `gplay compliance datasafety …`.

2. **A write-only `set` + offline `validate` doublet — no `pull`, no
   online dry-run.** The missing `get` makes `pull` impossible and makes
   the ADR-0011 Additive sync model inapplicable (no diff exists). `set`
   replaces the whole declaration (verb chosen to match `testers set` /
   `apps details set`; `apply` is **rejected** — ADR-0011 defines `apply`
   as additive per-field upsert, which this is not).

3. **The Play Console CSV is the canonical on-disk artifact, adopted
   verbatim.** gplay does not re-model Google's evolving Data Safety
   schema into YAML/JSON — we own neither the schema nor a read endpoint
   to verify a generated CSV against (same instinct as
   [ADR-0007](./0007-raw-http-not-google-go-sdk.md)). Default path
   `./compliance/data-safety.csv`, overridable with `--file`.

4. **Safety model inherited from ADR-0011 item 4.** `set` is
   live-production-facing (a bad declaration blocks releases or
   misstates data practices), so it requires `--confirm`; `CI=true` never
   auto-confirms. `validate` is a pure offline file check (valid CSV,
   non-empty, rectangular rows, sane encoding, plus a **non-fatal**
   warning when the header diverges from a bundled reference template) —
   it makes **no semantic claim**, because only the real POST tells you
   whether Google accepts the declaration. `set` runs `validate`
   implicitly before posting. `set --dry-run` is the full rehearsal minus
   the POST (validate + resolve account/package + report the target).

## Consequences

- There is deliberately **no `compliance datasafety pull`** and no online
  dry-run. This ADR is the canonical answer to "why can't gplay show me
  the current Data Safety declaration?" — the API is write-only, not a
  gplay gap.
- The namespace name, the `datasafety set` / `validate` command names and
  their flags fall under the Public contract
  ([ADR-0010](./0010-versioning-public-contract-and-ga.md)) once GA.
- The `compliance` namespace is built to host the next regulatory surface
  (content rating) without re-litigation, the way ADR-0011 set `metadata`
  up to extend to images.
- `validate`'s structural-only stance means a CSV that passes can still
  be rejected by Google. The warning wording and docs must make that
  explicit so "validated" is not read as "accepted".
