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
| `10` | Authentication failure — service account invalid, token refused, scope missing | No |
| `11` | Authorization — HTTP 403, e.g. the service account was never invited on the app | No |
| `20` | Client-side validation — malformed AAB, unknown locale, oversized listing text | No |
| `30` | API 4xx other than auth/permissions — not found, conflict, gone | No |
| `40` | API 5xx — upstream temporarily unhealthy | **Yes** |
| `50` | Network — timeout, DNS, connection refused | **Yes** |
| `60` | State conflict — another Edit open, rate-limited, ambiguous release target | Sometimes |

The same table ships inside the binary: `gplay help exit-codes` is generated
from the same source the binary actually returns, so it can never drift.

## Retry pattern for CI

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

## Related

- [CI/CD guide](/docs/guides/ci-cd/)
- [gplay for AI agents](/docs/agents/agent-guide/)
