# ADR-0059: Edit lifecycle implementation

## Status

accepted (2026-11-20)

## Context

DESIGN §4 sets the user-facing Edit lifecycle: implicit Edits discarded on
failure, explicit Edits pinned in `.gplay/edit-<package>.json`, stale pins,
the review opt-ins, unknown commit outcomes, and which reads join a pinned
Edit. This ADR records what §4 leaves to the implementation
(`internal/play/edits`) and repeats none of it. These decisions lived in code
comments; the comment purge (the `scripts/lint/comments.py` ratchet) gathers
them here.

## Decision

### Cleanup

- **A failed cleanup is swallowed when a real cause exists.** When a write or
  its commit fails, the discard that follows may fail too; the user sees the
  first failure, which is the one to act on.
- **A failed cleanup is reported when nothing else failed.** A skip under
  `--if-exists skip` ([ADR-0050](./0050-if-exists-read-before-create.md)) and
  the `apps add` probe ([ADR-0006](./0006-apps-add-validates-via-api.md))
  surface a failed discard as a dangling-Edit error carrying the Edit ID:
  swallowing it would report success over an open Edit that blocks the next
  publish for up to 24 hours.
- **The discard never runs on the caller's context.** A cancelled or expired
  context must not also kill the cleanup, so the DELETE gets its own deadline:
  10 seconds, room for `--retry` to replay a 5xx, and 5 seconds once the
  caller's context is done (DESIGN §4, the CI kill margin).
- **A read-only Edit is never committed.** A commit without changes is not a
  no-op: at best it burns one of the daily publish slots Google rate-limits,
  at worst it mutates state. The same holds for a write that decided to write
  nothing (ADR-0050), whose implicit Edit is discarded instead.
- **The read-only discard is deferred**, so it runs on a normal return, an
  error, and a panic unwind, after which the panic keeps propagating.

### Dangling and conflicting Edits

- **An Edit left open by an implicit-mode failure is never pinned**
  (`--keep-edit-on-failure`), so `gplay edits discard` cannot find it. The
  error carries the Edit ID and points at what works: the Edit's expiry
  (about 24 hours) or the Play Console. It takes the wrapped failure's exit
  code, else 60.
- **The `editAlreadyExists` hint does not name `gplay edits discard` either**:
  the conflicting Edit may belong to another client, or to a pin in another
  repository.

### Commit

- **A 2xx commit is success whatever happens to its body.** The returned
  `AppEdit` is unused, and a response cut mid-body must not report a live
  publish as a failure.
- **An implicit Edit is still discarded after an unknown commit outcome.** If
  the commit landed, the Edit is gone and the DELETE is a harmless 404; if it
  did not, the DELETE frees the package for the next run.
- **A request that failed to build locally also reads as unknown.** Registry
  URLs make it unreachable, and erring toward "check first" is the safe side.
- **`--changes-not-sent-for-review` exists because some apps cannot commit any
  other way**, after a rejection for instance.
- **A release is never committed without its mapping.** `releases upload
  --mapping` uploads the ProGuard/R8 file in the same Edit once the
  versionCode is known; if that upload fails the whole command fails, so an
  implicit Edit is discarded and a pinned one stays open. A release whose
  crash stacks cannot be read is the failure `--mapping` exists to prevent
  (#250).

### Explicit Edits

- **`gplay edits commit` leaves the Edit and the pin in place on failure**, so
  the commit can be retried or the Edit discarded; the pin goes only after a
  successful commit.
- **`gplay edits discard` clears the pin whatever the DELETE answers.** An Edit
  that already expired or vanished server-side is the state the operator asked
  for.

## Consequences

- No gplay path leaves an Edit open without either the user asking for it
  (`--keep-edit-on-failure`, explicit mode) or an error naming its ID.
- §4 stays the user contract; a rule here may change without notice to users
  as long as §4 still holds.
