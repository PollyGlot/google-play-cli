# Android Publisher long-tail: surface placement and new domain verbs

## Status

accepted

## Context

[ADR-0026](0026-maximal-admin-api-coverage.md) puts every Play **admin** API in
scope. PRD #243 ("Android Publisher long tail") covers the four remaining
Android Publisher surfaces not owned by another PRD — **Internal App Sharing**
(`internalappsharingartifacts`), **App Recovery** (`apprecovery`), **Device
Tier Configs** (`applications.deviceTierConfigs`), and **Expansion Files / OBB**
(`edits.expansionfiles`). Each needs two decisions that are cheap to get wrong
and expensive to rename once the companion skills (#53 / ADR-0021) pin to them:

1. **Where does it live** — a new top-level namespace, or a grouping noun under
   an existing one?
2. **What verbs does it carry** — [ADR-0019](0019-canonical-verb-vocabulary.md)
   froze the verb vocabulary, and three of these gestures (`deploy`, `cancel`,
   audience-widening) have no member of the frozen set. ADR-0019 §2 anticipated
   this: the **domain-verb category** admits a new verb that "names a real
   gesture `set`/`create`/`view` could not state honestly," gated by an
   admission test that "keeps the category from becoming a catch-all." New
   domain verbs are *additive* (no rename, no existing dependents), so admitting
   them breaks no skill or contract.

The deciding lens, as in ADR-0019, is **agent legibility**: the placement must
follow the same predictable rule across surfaces so an agent never re-litigates
"new namespace or not?" and so the verb for a gesture is the one Google's own
method name suggests.

## Decision

### 1. Surface placement (the Edit-lifecycle test)

The rule that decides "new top-level namespace vs grouping noun under an
existing one" is **whether the resource rides the Edit lifecycle**, matching the
precedent that `releases mappings` lives under `releases` *because* a Mapping is
an androidpublisher Edit upload ([ADR-0027](0027-vitals-second-service-scope-readonly.md) / CONTEXT.md "Mapping").

| Surface | Edit resource? | Placement | Verbs |
|---|---|---|---|
| Internal App Sharing | **No** (`internalappsharingartifacts`, no `editId`) | `releases sharing upload` — grouping noun under `releases` | `upload` |
| Expansion Files (OBB) | **Yes** (`edits.expansionfiles`, keyed by `apkVersionCode`) | `releases expansion-files upload/set/view` — grouping noun under `releases` | `upload`, `set`, `view` |
| App Recovery | **No** (`apprecovery`, own `appRecoveryId`, draft→active→canceled) | `recovery …` — **new top-level namespace** | `create`, `list`, `deploy`, `cancel`, `add-targeting` |
| Device Tier Configs | **No** (`applications.deviceTierConfigs`, server-assigned id) | `device-tiers …` — **new top-level namespace** | `create`, `view`, `list` |

- **Internal App Sharing → under `releases`.** It is a non-track *upload* that
  bypasses the Edit lifecycle; the channel distinction lives in the `sharing`
  noun, mirroring `releases mappings`. `share` is **not** invented as a verb —
  the API methods are `uploadapk`/`uploadbundle` and the canonical `upload`
  states the gesture honestly.
- **Expansion Files → under `releases`.** An ExpansionFile is an Edit artifact
  keyed by `apkVersionCode` — structurally a sibling of Mapping — so it follows
  the Mapping precedent verbatim. The `edits.*` lifecycle stays plumbing; there
  is no user-facing `edits` namespace.
- **App Recovery → new top-level `recovery`.** It is the *inverse* of the
  Mapping case: a non-Edit resource with its own id and lifecycle. `releases` is
  uniformly Edit-based, so the same rule that *places* Mapping under `releases`
  *excludes* recovery from it. Sibling-resource → sibling-namespace, like
  `tracks`/`testers`.
- **Device Tier Configs → new top-level `device-tiers`.** Not under `tracks`
  (no `editId`, not an Edit resource); not under `apps` (the *local* registry,
  whose `add`/`remove` grammar exists precisely because gplay cannot *create* an
  app on Play, whereas a DeviceTierConfig **is** created server-side). An
  app-scoped Publisher resource family parallel to `compliance`/`testers`.

### 2. Three new domain verbs (admitted under ADR-0019 §2)

The `recovery` namespace adds three verbs to the ADR-0019 domain-verb list, each
passing the §2 admission test. They are recorded here and in the normative
DESIGN §0 verb table.

- **`deploy`** — activate a *draft* recovery and push impacted users to a safe
  app version. Not `create` (the draft already exists) nor `set` (it is not a
  declarative state write); it is the act of going live. Mirrors the API method
  `apprecovery.deploy`.
- **`cancel`** — terminate an *active* recovery. Not `remove`: the resource is
  not deleted — it persists with `status=CANCELED` and cannot resume. Mirrors
  `apprecovery.cancel`.
- **`add-targeting`** — widen a recovery's audience. The API's `addTargeting`
  is *append-only* (it can only widen, never narrow), so `set` would lie
  (implying replace/narrow) and a bare collection-membership `add` would hide
  the targeting object. The first **hyphenated** domain verb in gplay; justified
  for legibility — an agent reading Google's `addTargeting` types
  `add-targeting`. Recorded as a deliberate, narrow exception to the single-word
  pattern, not a precedent for hyphenated verbs generally.

`scripts/verb-gate.sh` only blocks the five pre-rename names from ADR-0019; it
does not validate against an allow-list, so these new names pass mechanically.
Governance is documentary — this ADR plus the DESIGN §0 table.

### 3. Cross-cutting conventions (no new ADR each)

All four surfaces ship `[experimental]` first ([ADR-0010](0010-versioning-public-contract-and-ga.md)),
use raw HTTP ([ADR-0007](0007-raw-http-not-google-go-sdk.md)), pass `--output
json` through verbatim ([ADR-0003](0003-json-passthrough.md)), and follow the
write-safety tiers of [ADR-0017](0017-write-safety-and-agent-resolvable-refusals.md):
the additive/draft writes are ROUTINE (`--dry-run` only, `MarkMutating` for
`GPLAY_READONLY`), while the production-impacting `recovery deploy`/`cancel`/`add-targeting`
are DESTRUCTIVE (`--confirm`, exit 3 if missing). New glossary terms (Internal
App Sharing, Sharing artifact, Recovery/Targeting, Device tier config, Expansion
file) go in CONTEXT.md; new `area:*` labels `area:recovery` and
`area:device-tiers` are created for triage.

## Consequences

- The four surfaces ship without re-litigating placement: the Edit-lifecycle
  test decides, and future Publisher surfaces inherit it.
- The ADR-0019 domain-verb list grows by three (`deploy`, `cancel`,
  `add-targeting`); the DESIGN §0 table is the normative copy. No rename, so no
  skill or Public-contract breakage.
- `add-targeting` is the lone hyphenated domain verb; if a later surface needs a
  multi-word gesture, it is weighed against a `<noun> <verb>` grouping instead,
  not treated as licence for hyphenated verbs.

## Considered options

- **`releases share` for Internal App Sharing.** Rejected: `share` is not a
  canonical verb and inventing it violates the ADR-0019 freeze; the gesture is
  an `upload`, and the channel belongs in a noun.
- **App Recovery under `releases` (`releases recovery …`).** Rejected: recovery
  is non-Edit with its own lifecycle; folding it under the uniformly-Edit-based
  `releases` contradicts the very rule that places Mapping there.
- **Device Tier Configs under `tracks` or `apps`.** Rejected: no `editId` (not a
  tracks/Edit resource), and it creates a server-side object (not local-registry
  membership, so not `apps add`).
- **`recovery widen` instead of `add-targeting`.** Rejected: "widen" loses the
  object (widen *what?*); `add-targeting` mirrors the API method and reads
  unambiguously to an agent.
- **A separate ADR per namespace.** Rejected: only the verb admissions and the
  placement rule are load-bearing; one ADR records them for the whole PRD
  (repo-frugality), with CONTEXT.md/DESIGN.md carrying the per-surface detail.
