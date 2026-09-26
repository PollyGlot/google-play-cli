---
title: Exit codes
description: "gplay's semantic exit code taxonomy: which failures are retry-safe (API 5xx, network) and which are terminal, so CI scripts and agents can decide without parsing error text."
sidebar:
  order: 5
---

gplay's exit codes are **semantic**: the code alone tells a script or an
agent whether retrying can help, without parsing error messages.

<!-- BEGIN GENERATED exit-codes (make docs-update) -->
| Code | Meaning | Retry-safe? |
| --- | --- | --- |
| `0` | Success | n/a |
| `1` | Generic error (fallback when nothing more specific fits) | No |
| `2` | CLI misuse (unknown flag, bad value, repeated single-value flag, wrong number of positional args) | No |
| `3` | Safety flag required: well-formed, but a named --confirm/--grant-admin is missing; the message names it | Re-run with the named flag |
| `4` | Denied by environment policy (GPLAY_READONLY): a mutating command was refused | No; not resolvable by a flag, change the environment |
| `10` | Authentication failure (SA invalid, token refused, scope missing, no developer-id) | No |
| `11` | Authorization (403: SA not invited on the app/account) | No |
| `20` | Client-side validation (malformed AAB, unknown locale, ...) | No |
| `30` | API 4xx other than auth/perms (not found, conflict, gone, ...) | No |
| `40` | API 5xx (upstream temporarily unhealthy) | **Yes** |
| `50` | Network (timeout, DNS, refused) | **Yes** |
| `60` | State conflict (open edit, rate-limited, ambiguous target, ...) | Sometimes |
| `70` | Findings present (a read-only check command completed and reported drift; NOT an error) | No |
<!-- END GENERATED exit-codes -->

This table is generated from the catalog in the binary (`internal/exit`), the
same one `gplay exit-codes` prints, and a test fails the build when the two
disagree.

## Diagnostic codes

An exit code says which *bucket* a failure fell into; a **diagnostic code**
says which failure it was. Under `--output json` every failure carries one in
the error envelope's `code` field, next to a `retryable` bit, so an agent
branches on `EDIT_ALREADY_EXISTS` instead of matching the word "already" in the
message (see [Output formats](/docs/concepts/output-formats/)).

<!-- BEGIN GENERATED diagnostic-codes (make docs-update) -->
| Code | Exit | Retryable | Meaning |
| --- | --- | --- | --- |
| `GENERIC_ERROR` | `1` | No | Unclassified failure (no typed exit code); consult the message |
| `USAGE_ERROR` | `2` | No | CLI misuse: unknown flag, bad value, wrong number of positional args |
| `SAFETY_FLAG_REQUIRED` | `3` | No | A named safety flag is missing; re-run with the flag in `requires` |
| `POLICY_READONLY` | `4` | No | Refused by the read-only environment policy; not resolvable by a flag |
| `AUTH_FAILED` | `10` | No | Authentication failed: no Account, invalid credential, token refused |
| `PERMISSION_DENIED` | `11` | No | Authorization failed (403): the Account is not invited on this app |
| `VALIDATION_FAILED` | `20` | No | Client-side validation rejected the input before the API accepted it |
| `INVALID_ARGUMENT` | `30` | No | The API rejected the request as malformed (400) |
| `NOT_FOUND` | `30` | No | The API found no such package, track, Edit or resource (404) |
| `BASE_PLAN_NOT_DRAFT` | `30` | No | The API only deletes a DRAFT base plan; deactivate it first (state: INACTIVE), apply, then remove it |
| `API_ERROR` | `30` | No | Other API 4xx rejection |
| `UPSTREAM_UNAVAILABLE` | `40` | **Yes** | The API is temporarily unhealthy (5xx); retry |
| `NETWORK_ERROR` | `50` | **Yes** | Network failure with no HTTP response: timeout, DNS, refused |
| `STATE_CONFLICT` | `60` | No | Remote state conflicts with the request (409) |
| `EDIT_ALREADY_EXISTS` | `60` | No | An Edit is already open on this package; commit or delete it first |
| `EDIT_EXPIRED` | `60` | No | The pinned Edit expired; begin a new Edit and replay the mutation |
| `RATE_LIMIT_EXCEEDED` | `60` | **Yes** | Rate or quota limit exceeded; back off and retry |
| `FINDINGS_PRESENT` | `70` | No | A read-only check command completed and reported findings; not a failure |
<!-- END GENERATED diagnostic-codes -->

The Exit column is the *canonical* bucket, not a promise of equality: a few
wrapped errors keep a narrower exit code, and the envelope's own `exitCode` is
authoritative for a given failure. Codes are append-only: a new failure mode
earns a new code, an existing one is never renamed or repurposed. The same
catalog is available offline, `gplay exit-codes` for a person and
`gplay schema --codes --output json` for a program.

## Built-in retry with `--retry`

The transient classes (`40`, `50`, and a rate-limited `60`) are exactly what
the global `--retry N` flag handles for you, so you rarely need a hand-rolled
loop:

```bash
# Retry transient failures up to 3 times with exponential backoff + jitter
# (429 honors Retry-After). With --retry, --timeout bounds each attempt.
gplay releases upload app.aab --track internal --retry 3 --timeout 2m
```

`--retry` defaults to `0` (no retry). It **never** retries non-transient 4xx
(auth, validation) or `edits.commit`, where a duplicate commit could
double-publish, and it replays a non-idempotent write (an upload, a create, a
refund) only when the request provably never reached Google, so it is safe to
leave on. `Retry-After` is honoured up to the 30s maximum backoff. `--timeout` defaults to 60s for
control-plane calls and is unbounded for uploads; with `--retry` it becomes
a per-attempt bound. A deadline-exceeded failure maps to exit `50`, so the
same wrapper that retries a network blip retries a timeout.

## Hand-rolled retry across commands

When you need shell-level control (retrying across *separate* commands, or
adding alerting), branch on the exit code yourself:

```bash
for attempt in 1 2 3; do
  gplay releases upload app.aab --track internal
  code=$?
  case $code in
    0)     exit 0 ;;
    40|50) echo "transient (exit $code), retrying..."; sleep $((attempt * 10)) ;;
    *)     exit $code ;;
  esac
done
exit 1
```

## Exit 3: machine-resolvable refusals

Code `3` is designed for agents: the command was valid, but a deliberate
safety acknowledgment is missing (for example `--confirm` on a production
publish, or `--grant-admin` when granting admin). The error message names
the exact flag, so an automated caller can decide to re-run with it. It is a
*resolvable* refusal rather than a dead end.

## Exit 4: environment-enforced refusals

Code `4` is the opposite of `3`: it is **not** resolvable by adding a flag. A
mutating command was refused because `GPLAY_READONLY` is set in the
environment, an authority boundary a harness can impose on an agent
independent of the flags the agent chooses. The fix is to change the
environment, not the command. See
[gplay for AI agents](/docs/agents/agent-guide/).

## Related

- [CI/CD guide](/docs/guides/ci-cd/)
- [gplay for AI agents](/docs/agents/agent-guide/)
- [Stability and the Public contract](/docs/concepts/stability/): exit codes
  are part of the frozen contract, for the commands *not* marked
  `[experimental]`
- [Migrating to 1.0](/docs/guides/migrate-to-1-0/): missing `--confirm`
  refusals moved from `2` to `3`
