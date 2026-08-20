# `EnsureAccount` surfaces invalid-credential errors; an absent credential stays benign

## Status

accepted

## Context

The lazy-probe change (PR #172, commit `ca77148`) deferred the OS-keyring
probe and credential load until a command actually needs a token, but it
preserved a pre-existing contract: `kernel.RunContext.EnsureAccount` was
**best-effort** — any error from `resolver.Resolve` (no source, malformed
JSON, unreadable file, keystore read failure) was discarded and collapsed
to `rc.Account == nil`. The old eager `buildRunContext` already did
`sa, name, _ := resolver.ResolveWithName(...)`, error dropped by design.
The documented rationale was that credential *diagnostics* belong to
`gplay auth doctor`, not to the resolution path.

That swallow conflates two genuinely different situations behind one
`nil` Account:

- **Absent** — no credential is configured at all (`resolver.ErrNoSource`),
  or the named/active Account has no key in the store
  (`keystore.ErrNotFound`: logged-out, or never stored).
- **Invalid** — a credential *was* provided but its bytes are unusable:
  malformed JSON, a missing required field, an unreadable file, or a
  non-`NotFound` keystore/IO error.

Because both collapse to `nil`, the **same broken input** yields a
different outcome per command: `AuthedClient` → exit 10 with a message
that masks the cause; `auth status` → exit 0, "No active account"
(silently OK); `auth doctor` → a synthetic generic "no active account"
check-1 failure; `apps list/add/remove` → a silent fall-back to
`rc.Resolved.ConfigAccount` (so a malformed inline `--service-account`
lists *another* Account's packages). That inconsistency is the
motivation.

This ADR consciously **reverses** the documented swallow (also recorded
as a CodeRabbit review learning on PR #172). The reversal is small in
spirit: `auth doctor`'s `internal/auth/doctor.ResolutionFailure(err)` was
*already* built to render the real resolution error as its check-1 hint —
it was simply being fed a synthetic `errors.New("no active account")`
instead of the cause `EnsureAccount` had thrown away. The handler for the
error already exists; this ADR just stops starving it.

The conventional CLI shape for this is settled: `auth status` exits 0 for
an absent credential and **non-zero with the underlying cause in the
message** for a corrupt one, and a `doctor` command folds the parse
failure into its checklist rather than aborting. Same shape as the
decision below. gplay adds two things that convention usually lacks: a
semantic exit taxonomy (rather than a blanket exit `1` for every invalid
credential), and treating an empty required field as *invalid* rather
than *absent*, see Considered options.

## Decision

1. **Split absent from invalid.** `EnsureAccount() error` returns `nil`
   for the **absent** bucket (`resolver.ErrNoSource` **or**
   `keystore.ErrNotFound`), leaving `rc.Account == nil` — the existing
   signal `status`/`doctor`/`apps list` already key off. For the
   **invalid** bucket it returns the error (memoised on `accountErr`,
   `rc.Account` still `nil`).

2. **Invalid credential = exit `10`, guaranteed at one chokepoint.**
   `EnsureAccount` wraps any non-absent error in a kernel-level
   `credentialError{cause}` carrying `ExitCode() int = 10` and the message
   `could not read credential: <cause>`, wrapping with `%w` so the typed
   cause (e.g. `serviceaccount.MissingFieldError` and its field name) stays
   readable and `errors.As` keeps working. The guarantee lives in this
   single place rather than being scattered as `Coder` types across
   `serviceaccount` and `keystore`.

3. **Per-caller reconciliation** (all follow from 1–2):
   - **`AuthedClient`** propagates the invalid error (real message +
     exit 10); the absent case keeps its `authError` (exit 10, "run
     `gplay auth login`").
   - **`auth status`** returns the error (exit 10, no payload) for a
     corrupt active credential; **absent is unchanged** — "No active
     account", exit 0.
   - **`auth doctor`** feeds the real error to `ResolutionFailure(err)` so
     check 1 shows the cause; it renders the checklist itself, so there is
     no double-report. Absent still synthesises the generic "no active
     account" check.
   - **`apps add/list/remove`** surface the invalid error in their
     `AccountName == ""` branch instead of falling back to
     `ConfigAccount` — killing the silent-fallback bug for a malformed
     inline credential.

4. **Idempotent and test-safe.** Repeated calls return the memoised
   `accountErr` (guarded by `accountDone`). A hand-built `RunContext`
   (`NewForTest`, `lazy == false`) stays a no-op and returns `nil`; tests
   keep assigning `rc.Account` directly.

## Considered options

- **Keep swallowing (status quo).** Rejected: one broken input produces
  three different outcomes across commands, and a corrupt credential is
  masked as "no account".
- **Treat `ErrNoSource`/`ErrNotFound` as invalid too (uniform hard
  error).** Rejected: it regresses `auth status` (exit 0 → non-zero) and
  read-only `apps list` for every logged-out or unconfigured user. "Absent"
  is a legitimate benign state, not a failure.
- **Treat a missing required field as *absent*.**
  Rejected: gplay's `MissingFieldError` fires from `serviceaccount.Parse`
  on a credential the user actually *provided* — an inline
  `--service-account` that is broken right now, or stored bytes that are
  corrupt (`auth login` validates before storing, so a stored Account
  cannot be "half-configured"). "Invalid" is the accurate read, and
  the "run `auth login`" advice that pairs with *absent* would misdirect
  the inline path. Missing-field-is-absent holds only when the trigger is
  an empty *config template field*, a different data model.
- **Scatter `Coder` types at each boundary (`serviceaccount` +
  `keystore`).** Rejected: it spreads the exit-10 guarantee across
  packages and risks a missed path; the kernel resolution step is the
  natural single boundary for the whole credential subsystem.
- **Exit `20` (client-side validation) for a malformed SA JSON.**
  Rejected: DESIGN §9 row `10` names "SA invalid"; `20` is artifact /
  argument validation (malformed AAB, unknown locale). [ADR-0015](./0015-developer-account-addressing-rides-on-account.md)
  set the precedent that a credential-configuration gap is auth-family
  (`10`), not usage/validation.

## Consequences

- **User-visible:** `auth status` exits `10` (was `0`) when the active
  credential is present-but-corrupt. Otherwise additive — the absent path
  is unchanged. No new exit code is invented; `10` is already in the
  Public contract (DESIGN §9 / [ADR-0010](./0010-versioning-public-contract-and-ga.md)).
- `apps list/add/remove` stop silently falling back to `ConfigAccount` on
  a malformed inline `--service-account`; they fail with exit `10` and the
  cause.
- `auth doctor` shows the real cause in check 1 instead of a generic "no
  active account".
- Command tests that assert `rc.Account == nil` semantics and specific
  exit codes will ripple. `NewForTest` (`lazy == false`) keeps
  `EnsureAccount` a no-op returning `nil`, so hand-built `RunContext`s are
  unaffected.
- This ADR is the canonical answer to *"why does `EnsureAccount` return an
  error now, when the comment used to say best-effort swallow?"* — it is a
  deliberate consistency fix, not a regression to revert.
