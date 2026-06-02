# Developer-account addressing rides on the Account, not the project pin

## Status

accepted

## Context

`gplay team` (PRD #147) is the first surface keyed by the **Developer
account** — the Play Console org, addressed as `developers/{developerId}`
— rather than by a package or a locale. Every other write gplay makes is
package-scoped and resolves its target through the [ADR-0004](./0004-cascading-config.md)
cascade, where the package pin lives in the **committed** project layer
(`<repo>/.gplay/config.json`). The obvious move is to resolve a new
`--developer-id` "exactly like `--package`": flag → committed project
layer → error.

That obvious move is wrong, on two grounds — one an axis mistake, one a
hard API fact.

1. **Wrong axis.** A package is a property of the **repo** (a repo builds
   one app), which is why the committed, team-shared layer is its home. A
   `developerId` is a property of the **credential**: a Google service
   account is *invited into* a Developer account on the Play Console, and
   the org you administer is determined by which key you present, not by
   which repo you stand in. Pinning it in the committed layer repeats
   exactly the mistake ADR-0004 already forbids for the `account` field —
   *"machine-/credential-bound identifiers, pinning one in committed state
   breaks teammates."*

2. **It cannot be discovered.** Unlike App Store Connect — whose API key
   is *issued by* one team, so `GET /v1/users` and `GET /v1/apps` work
   with no team ID — the Google Play Developer API offers **no discovery
   path** for a credential's org. Verified against the REST reference:
   there is **no `developers` resource** (no list/get/whoami), **no
   `applications.list`**, and `users.list` **requires** the
   `developers/{developerId}` path parameter. The `developerId` is only
   obtainable manually, from the Play Console URL. This is the same
   limitation that already forces `apps list` to read from a **local
   registry** (`Account.Packages`) instead of the API.

Because the id can be neither derived from the credential nor enumerated
from the API, it **must** be supplied and recorded once. The only
question is *where* — and (1) answers it: with the credential.

## Decision

1. **`developer-id` is a property of the Account.** It is stored on the
   global Account record as `Account.DeveloperID` (`omitempty`) — the same
   move that `Account.Packages` already made to absorb the missing
   `applications.list`, and the "future per-Account registries" slot
   ADR-0004 reserved on the global layer. Captured at `auth login
   --developer-id …`, or persisted **type-once** the first time a `team`
   command runs with an explicit `--developer-id` against an Account that
   lacks one (mirroring how `apps add` populates `Packages`).

2. **Resolution order** (later wins, mirroring ADR-0004):
   `active Account.DeveloperID` → project-**local** (`config.local.json`,
   gitignored) → `GPLAY_DEVELOPER_ID` → `--developer-id` flag.

3. **Forbidden in the committed layer.** `<repo>/.gplay/config.json` must
   not carry a `developer-id`, rejected at parse time exactly like the
   `account` field — same rule, same reason (a credential/org identifier
   is not shared repo state). The project-**local** override exists only
   for the multi-tenant/agency case (one service account invited into
   several orgs), where it stays out of version control.

4. **Unresolved id is an auth-family failure (exit 10), not usage (2).**
   A missing developer-id is a credential-configuration gap, like a
   missing Account — not a malformed invocation. The error names how to
   set it (`auth login --developer-id` / `GPLAY_DEVELOPER_ID`).

## Considered options

- **Resolve like `--package` (committed project pin).** Rejected: wrong
  axis (org follows the credential, not the repo) and it reintroduces the
  committed credential-pin that ADR-0004 outlaws for `account`.
- **A new top-level config axis, independent of the Account.** Rejected:
  it creates a second source of truth that can disagree with the active
  Account, for an id that is 1:1 with the credential in the common case.
  Keeping it on the Account makes "switch Account" also switch org, which
  is the behaviour users expect.
- **Auto-discover at login (the App Store Connect model).** Rejected: not
  a choice — the API exposes no whoami/list to discover it (see Context).

## Consequences

- `Account` gains a backward-compatible `DeveloperID` field; pre-existing
  global configs round-trip unchanged (`omitempty`), as with `Packages`.
- The `--developer-id` flag/env name, the resolution precedence, and the
  "forbidden in committed config" rule fall under the **Public contract**
  ([ADR-0010](./0010-versioning-public-contract-and-ga.md)) once GA — the
  same status the config schema and Account-resolution precedence already
  hold.
- `auth doctor` gains a natural check: attempt `users.list` with the
  resolved id and confirm a 200 (and that the service account's own email
  appears among the returned Users). This is the closest gplay gets to a
  "whoami", and the canonical answer to "is my developer-id right?".
- This ADR is the canonical answer to *"why isn't `--developer-id`
  resolved from `.gplay/config.json` like `--package`?"* — it is an axis
  decision plus an API limitation, not an inconsistency to "fix".
