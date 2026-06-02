# Permission vocabulary: curated aliases, frozen role bundles, raw-enum escape hatch

## Status

accepted

## Context

`gplay team users` and `team grants` (PRD #147) write permissions. Google
expresses them as `CAN_*` enums on two near-parallel fields:
`developerAccountPermissions` (account-wide, `_GLOBAL`-suffixed) on a
**User**, and `appLevelPermissions` on a **Grant**. At the time of writing
the API exposes **20** grantable account-level values and **14**
app-level — plus a non-grantable `*_UNSPECIFIED` sentinel on each, and one
deprecated value each (`CAN_SEE_ALL_APPS`, `CAN_ACCESS_APP`). Those counts
are Google's, and they move: the PRD's own "21 + 15" was the
sentinel-inclusive count, already stale against the live 20 + 14.

Two facts shape the design. First, raw enums are hostile to humans and
agents (`CAN_MANAGE_PUBLIC_APKS_GLOBAL` is not how anyone expresses "let
them ship to production"). Second, gplay owns **neither** the enum set
**nor** a way to reconcile against it — the same footing as
[ADR-0007](./0007-raw-http-not-google-go-sdk.md) (hand-roll, don't depend
on the generated SDK) and [ADR-0014](./0014-compliance-namespace-datasafety-write-only.md)
(don't re-model an evolving Google schema). Re-modelling a vocabulary we
don't own risks turning gplay into a **gate** that blocks a valid
permission Google ships tomorrow.

## Decision

1. **Curated, scope-independent aliases.** One friendly name per
   meaningful permission — `release-production`, not the mechanical
   `manage-public-apks`. The same alias resolves to the `_GLOBAL`
   account-level enum under `team users` and the bare app-level enum under
   `team grants`; the module appends/strips `_GLOBAL` by scope.

2. **Raw-enum escape hatch.** Any literal `CAN_*` is **always** accepted,
   so a newly-added Google permission is grantable without waiting for a
   gplay release. Two guards: the `*_UNSPECIFIED` sentinels are
   **rejected**, and the two deprecated values are accepted **with a
   warning** steering to the modern equivalent. gplay is a convenience
   layer over Google's vocabulary, **never a gate** on it.

3. **Frozen role bundles.** gplay-defined, closed presets — `viewer`,
   `reviewer`, `tester-manager`, `release-manager`, `admin` — over a
   common read base (`view-app-info` + `view-app-quality`), built for
   least privilege. *Frozen*: a new Google enum **never** joins a bundle
   silently; membership changes only by an explicit, versioned gplay
   change. Selected with `--role`, **mutually exclusive** with the
   explicit `--permissions` list.

4. **Money and niche capabilities are bundle-excluded by design.**
   `view-financial` / `manage-orders` (sensitive) and the account-only
   Play-Games / managed-Play / connected-apps permissions are reachable
   only via explicit `--permissions` or a raw enum — never tucked inside a
   friendly role, so no role silently confers money or org-level control.

5. **`admin` is the single "all permissions" enum** (`CAN_MANAGE_PERMISSIONS[_GLOBAL]`)
   and is the one bundle that trips the extra write-safety gate (recorded
   separately with the write-safety decision): handing out full control is
   never silent.

6. **The vocabulary is published offline** by `gplay team permissions`
   (table + `--output json`) — the single source of truth the validator
   and the command share, tested once as golden output. The "unknown
   alias" usage error (exit 2) points the caller at it.

## Considered options

- **Mechanical aliases derived from enum names.** Rejected: ugly and
  inconsistent, and it *still* needs the escape hatch — curation costs
  little for ~30 values and reads far better.
- **Closed vocabulary (curated only; reject unknown enums).** Rejected:
  makes gplay a gate — a permission Google ships before gplay updates
  becomes un-grantable. The escape hatch removes that failure mode.
- **Evolving bundles (auto-absorb new related enums).** Rejected: silent
  privilege escalation. `--role admin` / `release-manager` broadening
  without the operator's knowledge is unacceptable on an access-control
  surface.
- **Pass enums through verbatim, no aliases or bundles.** Rejected:
  defeats the agent-first intent (US11) and the in-terminal discovery goal
  (US17).

## Consequences

- Alias names, role-bundle names **and their membership**, and the
  `team permissions` command are part of the **Public contract**
  ([ADR-0010](./0010-versioning-public-contract-and-ga.md)) once GA.
  Changing a bundle's membership is a contract event, reviewed as such.
- The alias/bundle table is hand-maintained Go, built by reading the
  official SDK / discovery doc as an **oracle** (not a dependency —
  ADR-0007). The escape hatch absorbs the lag between a Google addition
  and a gplay update.
- A passing alias/bundle means "well-formed", not "Google will accept it":
  only the real API write confirms acceptance (same caveat as
  `datasafety validate`, ADR-0014). The "unknown alias" path is offline;
  a *rejected-by-Google* permission surfaces as an `*api.Error`.
- A deprecated value used raw still works but warns; gplay never offers it
  as an alias, steering callers to the modern permission.
