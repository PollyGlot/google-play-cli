# Structured diagnostic codes in the JSON error envelope

## Status

accepted

## Context

[ADR-0023](./0023-json-error-envelope.md) gave failures a machine-readable
envelope carrying the semantic exit code, the human message, the upstream
`error.errors[].reason` values and the missing safety flag. That covered the
"where do I read it" question, but not the "what do I branch on" one.

The exit-code taxonomy (DESIGN §9) is deliberately coarse: it answers *how bad
is this and can I retry*, in twelve buckets. It cannot answer *which failure was
this*. Exit `60` is returned for an Edit that is already open, for a rate limit,
and for an ambiguous release target: three situations a caller resolves in
three different ways. The discriminating signal existed in two places, and
neither was reachable as a stable token:

- Google's own `reason` values travelled verbatim in `reasons[]`, but as an
  open-ended upstream vocabulary with no promise attached and no local
  equivalent for gplay's own failures.
- gplay's own local failure kinds (safety flag required, read-only policy,
  usage, validation) survived only as prose in the message.

So an agent or CI wrapper branching on cause had to regex stderr, which breaks
on any wording change, and had to keep a per-cause table to decide whether to
retry. See [PRD #447](https://github.com/PollyGlot/google-play-cli/issues/447).

## Decision

Every failure carries a **diagnostic code**: a stable SCREAMING_SNAKE token,
plus a **retryable** bit, both added to the ADR-0023 envelope alongside the
failing `operation` and `package`.

**One classifier, and it is total.** `exit.Classify` resolves a code in three
passes, most specific first:

1. an explicit `exit.Diagnoser` on the error (the diagnostic sibling of `Coder`:
   an error that knows a narrower code declares it next to its own definition),
2. for an upstream `*api.Error`, Google's `reason` when gplay has promoted it to
   a code, then the HTTP statuses whose meaning the exit bucket loses
   (`429`, `400`, `404`),
3. the exit-code table, which covers everything else.

The third pass is why there is nothing to police per error type. The ~100 typed
errors in the tree already declare an exit code; the table maps it; a new one is
classified the day it is written. There is no "unclassified error path" to leave
behind, and therefore no registry to hand-maintain. The completeness test walks
the catalog and a spread of failures and asserts the classifier is total and
never names a code outside the catalog.

**Codes are frozen and append-only** (ADR-0010/0042). A new failure mode earns a
new code; an existing one is never renamed or repurposed, so a consumer's
dispatch table survives every upgrade. Google's raw `reasons[]` still travel
verbatim, so a consumer can branch on a reason gplay has not promoted.

**The catalog is introspectable from the binary**, in both registers:
`gplay help exit-codes` prints it under the exit-code table for a human, and
`gplay schema --codes --output json` emits it for a machine. `schema` is already
gplay's offline, no-auth, no-network introspection surface, so a skill author has
one command to ask what the binary can tell them about itself.

**Boundaries.** Exit-code numbers, human message wording, stderr and the
`table` / `markdown` paths are all unchanged. The envelope stays on **stdout**
(ADR-0023's channel decision, unrevisited here) and stays failure-only.

## Consequences

- An agent branches on `code` and reads `retryable` instead of regexing stderr
  or carrying a cause-specific retry table.
- The code vocabulary is now versioned public contract; adding a code is
  compatible, renaming one is a major bump.
- The retryable bit is a property of the code, not of the call site. It is a
  coarse "can replaying this unchanged plausibly work", not a promise of
  idempotency: `STATE_CONFLICT` is marked not retryable even though the exit-60
  bucket is documented as "sometimes", because the caller must act (commit or
  discard the Edit) rather than simply retry.
- `internal/exit` now imports `internal/play/api`, since the classifier needs
  the canonical upstream error's status and reasons. The dependency runs one
  way (`api` knows nothing of `exit`), and it is what keeps the classification
  in one place instead of splitting it between the two packages.

## Alternatives considered

- **A per-error-type registry, with a test failing on any constructor without a
  code.** Rejected: it makes classification a second thing every one of ~100
  typed errors must remember, for a signal that is already derivable from the
  exit code they declare. The failure mode it guards against (a new error path
  ships unclassified) is eliminated by making classification total instead.
- **Emit the envelope on stderr.** Rejected in ADR-0023 and not reopened here:
  stderr is the human channel, stdout is the data channel, and moving the
  envelope now would break every existing consumer for no new capability.
- **Pass Google's `reason` through as the code.** Rejected: it is an upstream
  vocabulary gplay does not control and cannot freeze, it does not exist for
  local failures, and it is not SCREAMING_SNAKE. The raw reasons stay in
  `reasons[]`, which is the right place for an unfrozen signal.
- **A separate `gplay diagnostics` command for the catalog.** Rejected: a new
  top-level leaf for a static table, when `schema` is already the offline
  introspection surface.
