# ADR-0067: `apps audit`, a read-only sweep that reports Findings

## Status

accepted (2026-11-20)

## Context

`gplay apps audit` (PRD #449) sweeps a set of apps and reports Findings, so CI
can gate on "is every app in a clean state?" without clicking through the Play
Console app by app. DESIGN §9 sets its exit codes (70 for Findings, the
ordinary API code for an app it could not read), DESIGN §4 its concurrency and
its read-only Edits, DESIGN §7 its gplay-owned JSON and frozen check IDs, and
[ADR-0024](./0024-readonly-environment-policy.md) why it runs under
`GPLAY_READONLY`. The decisions behind its shape lived in code comments; the
comment purge (the `scripts/lint/comments.py` ratchet) gathers them here.

## Decision

### Naming and surface

- **`apps audit`, not `account audit`.** GLOSSARY reserves **Account** for a
  locally registered credential, and three different things end in "account"
  here. The sweep's unit is the app, so the command hangs off the `apps` noun
  with a reference/diagnostic verb, the
  [ADR-0019](./0019-canonical-verb-vocabulary.md) category that holds `doctor`
  and `validate`.
- **Composition, not a new surface.** Discovery is `apps.search`, the read
  behind `apps accessible list` ([ADR-0039](./0039-accessible-apps-vs-local-registry.md)),
  and each app is read through the existing read-only Edit path
  (`edits.tracks.list`, `edits.listings.list`). No new API method, no mutation:
  the only non-GET calls of a sweep are the open and discard of each app's
  throwaway Edit.
- **The report is a gplay-owned document**, since it composes several
  resources and [ADR-0003](./0003-json-passthrough.md) has no single body to
  mirror. That, and a check set that may still grow, is why the command ships
  `[experimental]`.

### Choosing the apps

- **Packages are positional and variadic**, like `apps add <pkg>...`, not a
  repeated `--package`. Everywhere else `--package` addresses one app, and
  giving the same flag accumulate semantics on one command is the ambiguity an
  agent trips on.
- **Discovery runs only when no package is named.** It costs a Reporting call
  and can return an org's hundreds of apps; an operator who manages three
  names them and pays for nothing else.
- **Discovery mints its own Reporting-scoped client** instead of annotating
  the command with `kernel.WithScope`: the sweep needs both scopes (Reporting
  to discover, androidpublisher to read each app) and a command carries one.
  A run with named packages never asks for a Reporting token.
- **A failed discovery names the escape hatch.** A credential can hold
  androidpublisher rights without the Reporting access `apps.search` needs
  (ADR-0039); that operator can still audit by naming the packages. The hint
  carries no exit code of its own, so the wrapped API error keeps it
  ([ADR-0058](./0058-api-failure-classification.md)).
- **Discovered packages are sorted**, so the sweep order is deterministic.

### The sweep

- **Quota is the real cost**: one Edit and two GETs per app. The two GETs of
  one app stay serial: apps are already read four at a time (DESIGN §4,
  Concurrent reads), and a nested fan-out would double the burst past that
  limit.
- **An app the sweep cannot read is data, not an abort.** It is reported
  inside the document, so an audit over forty apps does not die on the one the
  credential cannot see, and the run still exits with that failure's API code
  (DESIGN §9): an unread app is a hole, and "clean" is not something such a
  run may claim.
- **The cross-app context is built from the apps actually read.** Guessing at
  an unreadable app would turn its API failure into false `locale-drift`
  Findings on its peers.
- **The exit code of a sweep with failures follows non-retryable-wins**, the
  rule and the retryable set (40, 50; 60 excluded) of
  [ADR-0040](./0040-variadic-apps-add-partial-failure.md): a caller that sees
  40 or 50 knows a plain retry is worth it, anything else means inspect and
  fix.

### The report

- **The `ran` section names the apps and checks that executed.** Without it,
  "no Findings" cannot tell a clean set of apps from a sweep that covered
  nothing. The table and markdown views print it, with the per-app failures,
  under the Findings table for the same reason.
- **The command renders its report before returning a non-zero exit.** The
  kernel renders only on the success path, so a command that exits non-zero
  with data on stdout renders it itself, as `auth doctor` does. The exit-code
  decision is kept apart from the cobra wiring so it is testable without a
  keystore.

### Tests

- **The offline fake fails the test on any request that is not the token
  exchange, the Reporting search, a GET, or an Edit open or discard.** An
  audit that writes is a contract breach, not a wrong answer. The breach is
  reported through the test handle rather than only as a failed round trip,
  because the sweep folds a per-app transport error into its report and would
  carry on.

## Consequences

- A new check is a change to `internal/audit` and its ID joins the frozen
  vocabulary (DESIGN §7); it does not change the sweep's cost.
- A sweep's cost grows with the app count only; a check that needs another
  read per app changes the quota line above and belongs in this ADR.
