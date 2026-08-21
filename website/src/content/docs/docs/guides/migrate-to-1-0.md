---
title: Migrating to 1.0
description: "What changes when you upgrade gplay from 0.x to 1.0: one exit-code fix on missing --confirm refusals, and what the new Public contract promises."
sidebar:
  order: 9
---

Upgrading from `0.18.x` to `1.0.0` is a **one-line change at most**, and only
if your scripts branch on specific exit codes.

## The one breaking change

Commands that refuse because a required `--confirm` is missing now exit **3**
("safety flag required") instead of **2** ("CLI misuse").

Affected:

| Command | Refusal |
| --- | --- |
| `gplay releases upload` / `promote` / `rollout` | publishing to `production` (`--confirm` guards accidental rollouts) |
| `gplay metadata apply` | including the `--prune` gate |
| `gplay metadata images apply` | image slot reconciliation |
| `gplay auth logout` | removing a stored credential |
| `gplay compliance datasafety set` | pushing a Data Safety declaration |

**You are unaffected** if your scripts treat any non-zero exit as failure,
which is the common case.

### If you do branch on the exit code

```diff
 gplay auth logout ci-account
 case $? in
   0) echo "removed" ;;
-  2) echo "re-run with --confirm" ;;
+  3) echo "re-run with --confirm" ;;
 esac
```

Better still, stop branching on the number and read the flag out of the JSON
error envelope, which names what is missing:

```bash
gplay auth logout ci-account
```

Off a TTY the output is JSON by default ([Output
formats](/docs/concepts/output-formats/)). Do not rely on that in CI: a runner
can allocate a TTY. On the commands that accept it, pass `--output json`
explicitly when you intend to parse the result; `auth logout` has no such flag,
so it follows the TTY default alone.

```json
{
  "error": {
    "exitCode": 3,
    "message": "logout: --confirm is required to remove a credential (this operation is destructive)",
    "requires": ["confirm"]
  }
}
```

`requires` is populated for every exit-3 refusal, so a caller can react to the
named flag instead of matching on message text.

Note that branching on `2` was never a reliable way to detect this refusal:
`2` also covers typo'd flags, unknown packages, and invalid status combinations.
What 1.0 fixes is an ambiguous signal, not a working one.

See [Exit codes](/docs/concepts/exit-codes/) for the full taxonomy.

## What else changed

Nothing. No command was renamed, no flag was removed, no output shape changed.
The verb vocabulary was settled well before 1.0 and is unchanged.

## What 1.0 now promises

From this release on, the commands **not** marked `[experimental]` are covered
by the Public contract: their names, flags, semantics and exit codes cannot
change without a major version bump.

Commands marked `[experimental]` (the monetization catalog, `games`,
`recovery`, `orders`, and a handful of others) are shipped and supported but
stay free to evolve. Read [Stability and the Public
contract](/docs/concepts/stability/) before wiring one into a pipeline, and
check `--help` for the label:

```bash
gplay subscriptions --help
```

## Downgrading

`1.0.0` reads and writes the same config, credentials, and on-disk metadata as
`0.18.x`. Rolling back to the previous release is safe; the only difference you
will observe is the exit code above.
