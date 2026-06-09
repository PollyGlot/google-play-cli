---
title: Output formats
description: TTY-aware output in gplay — table for humans, JSON as raw API pass-through for machines, Markdown for docs and agents, and a strict stdout/stderr split.
sidebar:
  order: 4
---

Every read command renders its result in one of three **formats**, selected
with `--output table|json|markdown`. The default is `auto`:

- `CI=true` (non-empty) → `json`
- stdout is not a TTY (piped) → `json`
- otherwise → `table`

So the same command prints a human table in your terminal and clean JSON in
a pipe or CI — with `--output` as the explicit override when the
auto-detection isn't what you want (e.g. behind `tee`).

## `json` is API pass-through

For commands that wrap a Google Play Developer API call, `--output json`
returns the **API's native response shape**, including its per-endpoint
envelope (`{"reviews": [...]}`, `{"tracks": [...]}`). gplay adds no custom
envelope — the official Google API documentation *is* the schema
documentation for gplay's JSON output.

Two deliberate exceptions synthesise their own JSON, because they wrap no
API call: `gplay apps list` (a local registry; there is no `apps.list`
endpoint) and the offline reference commands (`team permissions`,
`schema`).

## `table`

Columns are chosen for readability, not pass-through. Each command's default
columns are documented in its `--help`, and `--columns col1,col2,...` lets
you override them.

## `markdown`

A first-class format, not "a table with pipes": tabular data renders as a
Markdown table, status output as `- **Field**: value` lines, and checklists
(`auth doctor`) as GitHub-style task lists. Useful for PR comments, docs,
and chat agents.

## stdout vs. stderr

The split is strict and scriptable:

- **stdout** carries data only — the requested output, nothing else.
- **stderr** carries logs, warnings, and errors. `-v`/`--verbose` adds
  info-level flow steps (the Edit ID, the deduced versionCode, each API
  call) and works in any position: `gplay -v auth status` or
  `gplay auth status -v`.

Errors are **never** pass-through. They print on stderr as:

```json
{"error": {"code": "<symbolic>", "message": "<human>", "details": {}}}
```

with any upstream API payload preserved inside `details`.

## Related

- [Exit codes](/docs/concepts/exit-codes/)
- [gplay for AI agents](/docs/agents/agent-guide/)
