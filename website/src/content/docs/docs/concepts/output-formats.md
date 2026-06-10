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

## Control-sequence sanitization (human formats only)

API strings are often user-generated (review text, store-listing copy). The
`table` and `markdown` renderers strip ANSI escape sequences and control
characters from every cell, so a hostile value can't inject colour, cursor,
or terminal-title sequences into your terminal or CI log. The stripping is
rune-based — accents, CJK, and emoji pass through untouched.

`--output json` is **never** sanitized: machine consumers get the bytes
verbatim (that's the pass-through promise). Fidelity lives on the JSON path,
safety on the human path.

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
- **stderr** carries logs, warnings, and errors. `--verbose` (short form
  `-v`) adds info-level flow steps (the Edit ID, the deduced versionCode,
  each API call) and works in any position: `gplay --verbose auth status` or
  `gplay auth status --verbose`.

Errors are **never** pass-through. A human-readable line always goes to
stderr. Under `--output json`, a failing command *additionally* writes one
structured envelope to **stdout**, so an agent or CI consumer can branch on
the failure without scraping stderr:

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

- `exitCode` and `message` are always present; `exitCode` mirrors the process
  [exit code](/docs/concepts/exit-codes/).
- `reasons` carries the upstream `error.errors[].reason` values when an API
  envelope was parsed; omitted otherwise.
- `requires` names the missing safety flag on an exit-3 refusal; omitted
  otherwise.

Under `table` / `markdown` a failure leaves stdout empty — the error goes to
stderr only. The envelope shape is part of gplay's public contract.

## Related

- [Exit codes](/docs/concepts/exit-codes/)
- [gplay for AI agents](/docs/agents/agent-guide/)
