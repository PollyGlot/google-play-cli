# Appstore hosted app surface: `$GPLAY_APP_STORE_PACKAGE` cascade and gate placement on submit, not create

## Status

accepted

amended (2026-11-20): implementation decisions moved from code comments; the commands are named as shipped (see Naming below)

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
hosted app record — per Google, "this must be called before any other RPCs for
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

## Amendment (2026-08-18, PRD #377 slices #379–#381)

Decision 2 above names the gate on `appstore update` `--yes`. That was the
wording carried over from the slice brief, not a naming decision: every other
safety gate in the CLI is `--confirm`, raised through
`exit.SafetyFlag("confirm", …)` for the exit-3 "a named safety flag is missing"
contract ([ADR-0017](./0017-write-safety-and-agent-resolvable-refusals.md) §1).

**`appstore update` ships `--confirm`.** Shipping `--yes` would have given the
CLI two names for one concept, forcing an agent to remember which surface uses
which. The *placement* decision — the gate rides on submit, not on create — is
unchanged and remains the point of this ADR. No `--yes` alias exists.

The same criterion resolved the other two gate questions in this batch:

- `appstore upload apk|image|policy` — **no gate**. Each hands Google a file and
  gets back a tracking id; nothing reaches a user until `appstore update` cites
  that id. Irreversible-but-inert.
- `appstore publish-status` — **no gate**. Withdrawing an app from the store is
  externally visible but *reversible*: the opposite call puts it back. The
  criterion requires a write to be both irreversible and externally visible;
  irreversibility is the property this one lacks.

## Naming

This ADR was written before the verbs settled. `appstore update` shipped as
`appstore submit` in 2.0.0 (the domain verb for an irrevocable submission to
review, [ADR-0051](./0051-removed-surface-names-its-replacement.md)), and in the
same release the uploads became `appstore apk upload`, `appstore image
upload` and `appstore policy upload`, and `appstore publish-status` took a
`set` verb. Read `appstore update` above as `appstore submit`: the gate
placement is unchanged.

## Implementation notes

- **A 404 is hinted by what the call addresses.** `appstore create` uses the
  review classifier, whose 404 names the store, since the hosted app record
  is what that call creates and cannot be the missing thing. The calls that
  act on an existing record (the three uploads, `publish-status set`,
  `submit`) use the hosted-app classifier, whose 404 names both the store and
  a missing record: Google requires `createappstorehostedapp` before any other
  call for a hosted app, so a caller who skipped `appstore create` lands
  there, and naming only the store would send them auditing a
  `--store-package` that is very likely correct. The wrapped API error keeps
  the exit code (404 is exit 30).

- **The submission body is the operator's JSON, forwarded verbatim** except
  `packageName`, which the resolved target governs: a `packageName` in the
  body that differs from `--package` or the Project pin is refused (exit 2),
  and with neither flag nor pin the body's `packageName` is the target. The
  call submits to review, so gplay never guesses between two packages. Decoding it into gplay's request struct and re-encoding it would drop
  anything the struct does not model, a field Google added last week or a
  misspelt key, and a submission cannot be recalled. Sent as written, a
  misspelling comes back as Google's rejection, never as a submission quietly
  missing half its content. The struct stays the documented shape, used to
  summarise the body.
- **Policy declaration responses stay raw JSON.** `PolicyResponse` is a
  seven-way oneof (boolean, document, group, keyed group, multiple choice,
  single choice, string) and Google adds question types over time. Modelling
  it would freeze today's snapshot into the CLI and force a release each time
  the questionnaire grows a variant.
