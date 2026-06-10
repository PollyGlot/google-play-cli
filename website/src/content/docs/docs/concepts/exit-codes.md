---
title: Exit codes
description: gplay's semantic exit code taxonomy — which failures are retry-safe (API 5xx, network) and which are terminal, so CI scripts and agents can decide without parsing error text.
sidebar:
  order: 5
---

gplay's exit codes are **semantic**: the code alone tells a script or an
agent whether retrying can help, without parsing error messages.

| Code | Meaning | Retry-safe? |
| --- | --- | --- |
| `0` | Success | — |
| `1` | Generic error (fallback when nothing more specific fits) | No |
| `2` | CLI misuse — unknown flag, bad value, missing required argument | No |
| `3` | Safety flag required — the command is well-formed but a named acknowledgment flag (`--confirm` / `--grant-admin`) is missing; the error names it | Deterministic: re-run with the named flag |
| `4` | Denied by environment policy — a mutating command was refused because `GPLAY_READONLY` is set; the message names the env var | No — **not** fixable by a flag; change the environment |
| `10` | Authentication failure — service account invalid, token refused, scope missing | No |
| `11` | Authorization — HTTP 403, e.g. the service account was never invited on the app | No |
| `20` | Client-side validation — malformed AAB, unknown locale, oversized listing text | No |
| `30` | API 4xx other than auth/permissions — not found, conflict, gone | No |
| `40` | API 5xx — upstream temporarily unhealthy | **Yes** |
| `50` | Network — timeout, DNS, connection refused | **Yes** |
| `60` | State conflict — another Edit open, rate-limited, ambiguous release target | Sometimes |

The same table ships inside the binary: `gplay help exit-codes` is generated
from the same source the binary actually returns, so it can never drift.

## Built-in retry — `--retry`

The transient classes (`40`, `50`, and a rate-limited `60`) are exactly what
the global `--retry N` flag handles for you, so you rarely need a hand-rolled
loop:

```bash
# Retry transient failures up to 3 times with exponential backoff + jitter
# (429 honors Retry-After). With --retry, --timeout bounds each attempt.
gplay releases upload app.aab --track internal --retry 3 --timeout 2m
```

`--retry` defaults to `0` (no retry). It **never** retries non-transient 4xx
(auth, validation) or `edits.commit` — a duplicate commit could
double-publish — so it is safe to leave on. `--timeout` defaults to 60s for
control-plane calls and is unbounded for uploads; with `--retry` it becomes
a per-attempt bound. A deadline-exceeded failure maps to exit `50`, so the
same wrapper that retries a network blip retries a timeout.

## Hand-rolled retry across commands

When you need shell-level control — retrying across *separate* commands, or
adding alerting — branch on the exit code yourself:

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

## Exit 3 — machine-resolvable refusals

Code `3` is designed for agents: the command was valid, but a deliberate
safety acknowledgment is missing (for example `--confirm` on a production
publish, or `--grant-admin` when granting admin). The error message names
the exact flag, so an automated caller can decide to re-run with it — a
*resolvable* refusal rather than a dead end.

## Exit 4 — environment-enforced refusals

Code `4` is the opposite of `3`: it is **not** resolvable by adding a flag. A
mutating command was refused because `GPLAY_READONLY` is set in the
environment — an authority boundary a harness can impose on an agent
independent of the flags the agent chooses. The fix is to change the
environment, not the command. See
[gplay for AI agents](/docs/agents/agent-guide/).

## Related

- [CI/CD guide](/docs/guides/ci-cd/)
- [gplay for AI agents](/docs/agents/agent-guide/)
