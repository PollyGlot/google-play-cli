# ADR-0058: API failure classification

## Status

accepted (2026-11-20)

amended (2026-11-20): implementation decisions moved from code comments

## Context

Every upstream failure reaches the user as an exit code and, under
`--output json`, a diagnostic code with a `retryable` verdict. The codes and
their meanings are DESIGN §9 and §9.1 (generated from `internal/exit`); this
ADR does not restate them. It records how a failure is sorted into them,
which the generated tables leave out, and the one question a failed write
must answer: may it have been applied? These rules lived in code comments;
the comment purge (the `scripts/lint/comments.py` ratchet) gathers them here.

## Decision

### From HTTP status to exit code

- **The status decides, through one table** (`api.StatusToExitCode`), with the
  exceptions below as the only refinements.
- **A 401 from the API is exit 10**, like a refusal from the token endpoint:
  re-running with the same credential fixes neither.
- **An artifact upload's 400 or 404 is exit 20.** On `bundles.upload` and
  `apks.upload` those statuses mean a malformed artifact (or, for an APK, an
  app that requires an AAB), a client-side problem, not the generic 30. Auth
  (403), conflict (409), 5xx and transport failures still win over this hint:
  a 403 on an upload is an auth problem.
- **An operator hint never moves an exit code.** The wrappers that attach
  the canonical hints to a 403 or 404 on the package axis carry no exit code
  of their own; the wrapped `*api.Error` stays authoritative through the
  `exit.Coder` chain. Command groups keep only their domain wording (a review
  id, an order, a track) and fall back to the shared hints for the generic
  package cases.
- **`editAlreadyExists` keeps the status's code.** On a 400 it exits 30, with
  the conflict hint; Google has shipped the same reason on a 429, which keeps
  its 60 (retry with backoff) rather than being downgraded
  ([ADR-0006](./0006-apps-add-validates-via-api.md)).
- **`releases promote` from an empty source track is exit 2**, not the exit 30
  of an absent resource: the user named a track that holds nothing to promote,
  an input error the API was never asked about. `rollout`, `halt`, `resume` and
  `complete` on a track with no matching release stay exit 30.

### From exit code to diagnostic code

- **The 400 and 404 refinements apply only on exit 30.** The artifact uploads
  remap those statuses to exit 20 (a malformed artifact); there
  `VALIDATION_FAILED` is the truthful code, and `NOT_FOUND` or
  `INVALID_ARGUMENT` would mislead.

### Failures without a status

- **A credential refusal is recognised by its RFC 6749 error code, not its
  status.** Google answers a deleted or disabled key, a bad JWT signature or a
  skewed clock with HTTP 400 `invalid_grant`, not 401, so the status alone
  misses the most common refusal. The error code is read from the body
  ([ADR-0062](./0062-credential-exposure.md), the token exchange).

- **A cause that carries its own exit code wins over the network bucket.**
  When the token exchange is refused, `oauth2.Transport` fails the request
  before it leaves, so the auth error (exit 10) arrives as a transport error;
  it must not surface as a retry-safe 50.
- **A 2xx whose body breaks mid-read is exit 50.** The server answered; the
  network failed (a reset, a deadline firing mid-body), so the failure joins
  the retry-safe bucket. The error is classified as a body-read failure (the
  Cloud Storage client gives it status 0), never from the 2xx already
  received.
- **A 2xx body that reads whole but does not decode is not yet classified one
  way.** Modules that decode by hand (edits, tracks, listings, images,
  testers, App Signing, voided purchases) tag the error with status 200, so it
  exits 30: the API answered something gplay cannot read, which a re-run does
  not fix. `api.DoJSON` and the modules built on it (vitals, team, one-time
  products, subscriptions, Cloud Storage, the app store uploads) leave the
  status unset, so the same failure exits 50. Moving a module from one to the
  other changes a public exit code; #844 tracks settling on one rule.
- **`auth doctor` watches the transport itself** to tell "never answered"
  from a key that could not be used. The jwt flow flattens the transport error
  with `%v` before returning it, so the error chain alone cannot draw that
  line. `RoundTrip` returns once the headers are in, so the body read is
  watched too: a read that dies after a 200 (a timeout, a reset) is the same
  "never answered".
- **A local file that is not a regular file is exit 20** (`LocalIOError`),
  never a transport 50 ([ADR-0056](./0056-media-upload-protocol.md)).
- **A Store image file that cannot be read after the scan is a `--dir`
  failure, exit 20.** `metadata images apply` lists the tree, then reads each
  image just before it needs it (DESIGN §4), so a file removed or made
  unreadable in between fails as it did when the whole tree was loaded
  upfront, not as an API error.
- **An image host's error is its status alone.** The `googleusercontent` host
  `metadata images pull` downloads from does not answer with Google's error
  envelope, so the message is built from the status. A 2xx that never
  arrived whole (cut mid-body, or past the image cap of
  [ADR-0057](./0057-bounded-responses-and-pagination.md)) is not the host's
  answer and carries no status: exit 50.
- **A downloaded image whose sha256 is not the stored one is exit 1.** Neither
  the network nor the API failed, and a retry would fetch the same bytes, so
  neither the retry-safe 50 nor an API code fits.

### Could the write have landed?

- **Only two outcomes prove a failed write was not applied**: a 4xx (429
  included: the request was refused before the write path), or a request that
  never left the machine. `api.NeverSent` draws the second line: a refused
  token exchange, a DNS failure or a dial failure. Any other transport failure
  may have reached Google's write path, and a 5xx or a broken 2xx body may
  have been applied.
- **The same line serves every caller that must answer the question**: the
  `--retry` transport before replaying a non-idempotent write, `edits.commit`
  before reporting `COMMIT_OUTCOME_UNKNOWN`
  ([ADR-0059](./0059-edit-lifecycle-implementation.md)), and the split
  monetization batches.
- **A batch split into several calls reports three sets** when one call
  fails: the items of the calls Play accepted, of the failing call, and of the
  calls never sent, plus whether the failing call may have been applied all
  the same. The error wraps the failing call's, so the exit code and the
  envelope stay the API's.
### Which requests `--retry` may send twice

DESIGN §8 sets what `--retry` retries; these are the rules for deciding that
a replay is safe.

- **Idempotency is a per-method fact declared in `internal/apiregistry`**,
  next to the verb and URL it derives from, not guessed from the path. A
  newly registered POST is non-idempotent until someone argues otherwise in a
  reviewed diff.
- **The verb sets the default.** GET, HEAD, PUT and DELETE are idempotent by
  HTTP semantics, and Google's PATCH (an update of the fields its mask names,
  `allowMissing` included) converges to the same state when sent twice. POST
  is not, and a verb gplay does not send is treated as not idempotent rather
  than guessed.
- **An allowlist of POSTs a replay cannot hurt** overrides the default, each
  entry justified: an entry is a promise that `--retry` may send the call
  twice. Read-shaped POSTs (a query riding a body, such as the vitals
  queries) qualify, and so does `edits.insert`: an Edit changes nothing until
  committed, so a duplicate opened by a replay is never used and expires.
  Leaving it out would make every Edit-scoped command lose `--retry` on its
  first call.
- **A refused token exchange is never replayed.** `oauth2.Transport` mints
  the token inside `RoundTrip`, so a revoked key surfaces as a transport
  error; replaying it re-sends a dead credential N times before failing the
  same way.
- **Backoff keeps a growing floor.** The delay is drawn from `[d/2, d]`
  rather than `[0, d]`, so successive waits still increase while the jitter
  breaks up synchronised retries.
- **A replayed request re-reads its body.** `net/http` derives `GetBody` and
  `ContentLength` only for in-memory readers, so a streamed body sets both
  itself: a replay reopens the source, and an upload is not sent chunked.

## Consequences

- A new failure inherits its code from the status and the cause it carries,
  with no dispatcher to extend (DESIGN §9.1).
- Unknown is the safe answer: a failure the rules cannot place on the "never
  applied" side is reported as possibly applied, and the user is told to
  check before re-running.
