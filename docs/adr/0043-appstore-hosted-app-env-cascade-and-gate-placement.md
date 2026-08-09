# Appstore hosted-app surface: `$GPLAY_APP_STORE_PACKAGE` cascade and gate placement on submit, not create

## Status

accepted

## Context

The `appstore` namespace (PRD [#377](https://github.com/PollyGlot/google-play-cli/issues/377),
API `appstoreappsreview`) introduces a third addressing axis: the **calling app
store** (`appstore/{appStorePackageName}`), alongside the package axis and the
developer-account axis ([ADR-0015](./0015-developer-account-addressing-rides-on-account.md)).
Every command in the namespace needs `--store-package`; a store operator running
several commands would repeat the same value every time.

The surface's entry point, `appstore create`
([#378](https://github.com/PollyGlot/google-play-cli/issues/378), PR
[#423](https://github.com/PollyGlot/google-play-cli/pull/423)), creates the
hosted-app record — per Google, "this must be called before any other RPCs for
this hosted app". The API exposes no read and no delete for that record.
Formally that matches the situation that made
[ADR-0032](./0032-custom-apps-account-axis-gated-creation.md) put `--confirm` on
`customapps create` ("creating an app record is irreversible, no delete
endpoint"), so the two decisions look contradictory unless the distinction is
written down.

## Decision

1. **`--store-package` cascades `flag > $GPLAY_APP_STORE_PACKAGE`, no config
   field.** The env var joins the `GPLAY_*` layer of the cascade
   ([ADR-0004](./0004-cascading-config.md)) for the whole namespace, so a store
   operator sets it once per shell or CI job. It does **not** get a
   `config.json` field: `.gplay/` pins the *hosted* project's package, and a
   repo pinned to one hosted app is not the place to persist the identity of
   the *calling store* — that is operator/session state, like `GPLAY_ACCOUNT`.
   A config field can be added later if real usage demands it; an env var
   cannot be removed once shipped.

2. **No confirmation gate on `appstore create`; the `--yes` gate rides on
   `appstore update`.** This refines the reading of
   [ADR-0017](./0017-write-safety-and-agent-resolvable-refusals.md)/ADR-0032
   without superseding them: the destructive/irreversible tier gates writes
   that are irreversible **and carry an external effect**. `customapps create`
   publishes a real app (binary included) into an organisation's managed Play —
   irreversible *and* externally visible. `appstore create` writes an inert
   prerequisite record: it submits nothing to review and publishes nothing;
   the externally visible act is `appstore update`, which assembles the
   details and submits them to Google's review — and *that* command carries
   the `--yes` gate ([#381](https://github.com/PollyGlot/google-play-cli/issues/381)).
   `create` keeps the ordinary write safeguards: `MarkMutating` (refused under
   `GPLAY_READONLY`, [ADR-0024](./0024-readonly-environment-policy.md)) and
   `--dry-run`.

3. **`--help` states the absence of delete.** Since the record cannot be
   removed, the command's help says so explicitly, so an agent does not go
   hunting for a `delete` verb that the API does not have.

## Consequences

- The gate criterion is now written: *irreversible + external effect* gates;
  *irreversible + inert* does not. Future surfaces with a create-prerequisite
  shape reuse this test instead of re-litigating ADR-0032.
- `$GPLAY_APP_STORE_PACKAGE` is documented in `DESIGN.md` §config alongside the
  other `GPLAY_*` vars; the whole namespace ships `[experimental]`
  ([ADR-0042](./0042-one-zero-ga-and-stability-label-mechanism.md)), so the
  cascade can still be adjusted before the label comes off.
