# JSON error envelope on failure under `--output json`

## Status

accepted

## Context

gplay is agent-first and CI-first. On success under `--output json` it emits
the raw Google Play API response (ADR-0003 pass-through). On **failure**,
however, stdout was empty: the error went to stderr as a human-oriented line
(`gplay: edits.commit on com.example.app: edit already exists (HTTP 409)
[reason: editAlreadyExists]`), and an automated caller had to scrape that text
to recover the one thing it needs — *why* the call failed and whether it is
retryable or fixable.

The structured signal already existed internally and was simply never
serialized:

- the **semantic exit code** (DESIGN §9) the process returns,
- the upstream **`error.errors[].reason`** values parsed off the API envelope
  (`api.Error.Reasons` — e.g. `editAlreadyExists`, `rateLimitExceeded`), which
  discriminate failure modes that share an HTTP status, and
- the **missing safety flag** on an exit-3 refusal (`exit.SafetyFlagError.Flag`)
  — the dry-run `requires` preview of [ADR-0017](./0017-write-safety-and-agent-resolvable-refusals.md),
  but available at *failure* time, not only on a rehearsal.

The shape gplay emits to stdout is part of the public contract
([ADR-0010](./0010-versioning-public-contract-and-ga.md)), so it warrants a
recorded decision before GA.

## Decision

When the resolved output format is `json` **and** a command fails, the kernel
writes exactly one object to stdout:

```json
{
  "error": {
    "exitCode": 60,
    "message": "edits.commit on com.example.app: edit already exists (HTTP 409) [reason: editAlreadyExists]",
    "reasons": ["editAlreadyExists"],
    "requires": ["confirm"]
  }
}
```

- `exitCode` (always) — mirrors the process exit code, so stdout and `$?` agree.
- `message` (always) — the same human string stderr carries.
- `reasons` (omitted when absent) — upstream `error.errors[].reason` values,
  extracted from `*api.Error` via `errors.As`.
- `requires` (omitted unless a safety refusal) — the missing acknowledgment
  flag(s) from `*exit.SafetyFlagError`, extending ADR-0017 to failure time.

**Boundaries that keep the change additive:**

- **stderr and exit codes are unchanged.** The human line and `exit.For(err)`
  are exactly what they were; the envelope is purely an *added* stdout payload.
- **Only `json` is affected.** `table` / `markdown` failures still leave stdout
  empty (error → stderr only). Success output is untouched (still ADR-0003
  pass-through).
- **Emitted once, and never on top of self-rendered output.** The kernel wraps
  stdout in a byte counter; the envelope is written only when the failing
  command produced no stdout of its own. This is what stops a self-rendering
  command — `auth doctor`, which prints its checklist then returns a failing
  error — from getting a second JSON object appended.
- **Best-effort.** A write failure while emitting the envelope is swallowed: the
  authoritative failure signal is the stderr line plus the exit code, both
  unaffected.

The envelope lives in `internal/output` (`output.WriteErrorEnvelope`,
`output.ErrorEnvelope`), the canonical place for output-format contracts, and
is emitted from `kernel.Run`.

## Consequences

- Agents and CI can branch on `exitCode` / `reasons` / `requires` from stdout
  without parsing stderr — exit-3 recovery (`requires`) and conflict handling
  (`reasons`, e.g. `editAlreadyExists`) become deterministic.
- The envelope is now a versioned part of the public contract: fields may be
  **added** compatibly, but `exitCode` / `message` / `reasons` / `requires`
  cannot be renamed or repurposed without a contract bump (ADR-0010).
- Failures that occur **before** format resolution (an invalid `--output`
  value, a config-load error at boot) cannot be JSON-enveloped — the JSON
  format is not yet known there — and surface as the plain stderr line. This is
  acceptable: those are usage/boot errors, not the API/safety failures agents
  branch on.
- Out of scope (PRD #206): publishing a JSON Schema for the envelope — a
  candidate follow-up alongside `gplay schema`.

## Alternatives considered

- **Wrap success output too (always an envelope).** Rejected: it breaks the
  ADR-0003 promise that success stdout is the raw API shape. The envelope is a
  failure-only addition.
- **Emit on stderr as JSON.** Rejected: stderr is the human channel (DESIGN §8);
  mixing a machine payload there forces consumers to parse a stream that also
  carries progress and warnings. stdout is the data channel.
- **Per-command annotation to suppress the envelope on self-rendering commands.**
  Rejected in favor of the byte-counter: it needs no per-command wiring and
  covers any future self-rendering command for free.
