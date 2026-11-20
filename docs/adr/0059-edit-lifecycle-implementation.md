# ADR-0059: Edit lifecycle implementation

## Status

accepted (2026-11-20)

amended (2026-11-20): implementation decisions moved from code comments

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

- **`gplay edits begin` refuses while a pin exists.** Play keeps one open Edit
  per app, so opening another would orphan the pinned one.
- **A pin that cannot be written discards the Edit just opened**, or it
  would hold the app for up to 24 hours with nothing local to find it by. If
  that discard fails too, the error names the open Edit ID and the ways out
  (the Play Console, the expiry). The pin-write failure owns the exit code:
  the discard error is only quoted, since a wrapped API error would take the
  code over.
- **`gplay edits commit` leaves the Edit and the pin in place on failure**, so
  the commit can be retried or the Edit discarded; the pin goes only after a
  successful commit.
- **A commit that succeeded but left its pin behind says so.** The error
  states that the change is live and names the stale pin file, so it does not
  read as a failed commit (inviting a retry) and the file, which would block
  the next `edits begin`, can be removed by hand.
- **`gplay edits discard` clears the pin whatever the DELETE answers.** An Edit
  that already expired or vanished server-side is the state the operator asked
  for, so a 404 counts as success. Even a transient failure clears the pin:
  the operator asked to abandon the Edit, and it expires on its own. When the
  DELETE and the pin removal both fail, the error carries both, so a local
  cleanup failure never hides whether the Edit is still open on Play.
- **The warning that a commit flag is ignored comes only before a staged
  write.** With a pinned Edit, a commit flag cannot apply and DESIGN §4 warns
  that the change is staged instead. A command that may stage nothing
  (`tracks create --if-exists skip`) emits that warning right before its
  write, so a skip never claims a change was staged.
- **A `--dry-run` never reads the pin**: the dry-run path never reuses a
  pinned Edit, so a corrupt pin must not fail a preview
  ([ADR-0063](./0063-offline-validation-before-the-api.md)). The online
  dry-runs of `metadata apply` and `metadata images apply` read the live
  Store front through a read-only Edit of their own, never the pinned one;
  only their real apply joins the pin.
- **An unusable pin is an error, never "no pin".** A missing file means
  implicit mode; a present file that is not JSON or has no `editId` must
  surface, because falling back to a fresh Edit would open a second,
  conflicting one while the real one stays open.
- **A pin naming another package is corruption**, the trace of a copied or
  renamed file: it is refused before the Edit is reused against the wrong
  package. A pin with no `package` field is tolerated (an older or minimal
  pin); the file name is the authority.
- **Only the content makes a pin corrupt.** A read that fails (permissions)
  or a path containment refuses is a different error, so `edits discard`
  removes a file only after reading it and finding it unusable.
- **A pin is written atomically** (a staging file renamed over it,
  [ADR-0060](./0060-path-containment-implementation.md)). A truncated pin
  would be refused by every write command and by `edits begin`, which reads
  the pin before it could overwrite it, so a crash mid-write would wedge the
  package until a manual `discard`.

### Concurrent reads

DESIGN §4 sets the user contract of the four-wide read fan-out
(`internal/fanout`); these are the rules behind it.

- **The error returned is deterministic**: the failure with the lowest index,
  the one the serial loop would have returned. Indices start in order and a
  started call always runs to completion, so every index below the first
  failure in time has finished when the fan-out returns. After a failure no
  new index starts; at most three calls already in flight finish.
- **A panic in a call is re-raised on the caller's goroutine** once every
  worker has stopped, so a deferred cleanup up the stack (the discard of a
  read-only Edit) still runs instead of the process dying on a worker
  goroutine with the Edit left open for 24 hours.
- **A call touches only the state of its own index**, or guards shared state
  itself; results land in slot `i`, which is what keeps the output in input
  order. The type system cannot check this, so a caller must.
- **Uneven work fans out in waves, not one task per unit.** `metadata images
  pull` lists every slot in one fan-out, then downloads every image of every
  slot in a second: a gallery of eight screenshots beside empty slots would
  otherwise hold one worker while the others idle.

## Consequences

- No gplay path leaves an Edit open without either the user asking for it
  (`--keep-edit-on-failure`, explicit mode) or an error naming its ID.
- §4 stays the user contract; a rule here may change without notice to users
  as long as §4 still holds.
