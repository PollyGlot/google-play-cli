# Context — gplay

Glossary of canonical terms for this project. Implementation details belong elsewhere (code, ADRs, README). This file is a glossary and nothing else.

## Terms

### gplay
The binary name of this CLI. Short, easy to type in CI scripts. Distinct from the existing TypeScript alternative `gpc` by yasserstudio and from `gpc` (GNU Pascal Compiler).

### Edit
A transactional unit on the Google Play Developer API. An edit is opened on a specific package, accumulates changes (metadata, listings, releases, tracks), and is committed atomically. Edits expire after ~24h and are exclusive per app (only one open at a time).

The CLI exposes edits in two modes:

- **Implicit** — most write commands (`gplay metadata apply`, `gplay releases upload`, etc.) open, modify, and commit their own edit in a single invocation. This is the default for the 90% case.
- **Explicit** — `gplay edits begin / commit / discard` lets the caller batch multiple changes into one transaction. While an explicit edit is open, its ID is persisted to `.gplay/edit-<package>.json` in the working directory. Subsequent write commands detect that file and reuse the open edit instead of creating their own.

We keep the term **edit** (Google's official vocabulary) rather than renaming to "transaction" or "changeset" — this matches the API docs and reduces friction for users reading both.

### Account
A named credential profile registered in gplay. One Account = one Google Cloud service account JSON. Multiple Accounts can coexist (different apps, different orgs, dev vs CI). Exactly one Account is **active** at a time and is used when no `--account` flag is passed. The active Account is recorded in the gplay config file; the credential itself lives in the OS keystore (or a 0600 fallback file).

Distinct from Google's "service account" (the GCP-side IAM principal): an `Account` in gplay is the **local registration** of a service account, with a human-friendly name and the active flag.

### Project
A repo-local pinning of a gplay invocation to a specific Android package. Created by `gplay init --package com.example.myapp`, which writes `.gplay/project.json` at the repo root. Any subsequent gplay command run inside that tree (walking up to find `.gplay/`) defaults its target to that package, so `--package` becomes optional.

A Project pins a **package only** — not an Account. Account resolution stays separate (config-wide active or env/flag).

Coexists in `.gplay/` with `edit-<package>.json` (open explicit Edit, see above). `.gplay/` is meant to be committed for `project.json` and gitignored for `edit-*.json` (transient).

### Format
A way of shaping a command's output for a specific reader: `table` for humans on a TTY, `json` for machines and scripts, `markdown` for documentation, chat agents, and PR comments. Selected by `--output`. The special value `auto` (the default, encoded as the empty string on the flag) lets the CLI pick based on context: `json` when stdout is not a TTY or when `CI=true`, `table` otherwise.

`json` is API pass-through (see [ADR-0003](./docs/adr/0003-json-passthrough.md)). `table` and `markdown` are human-shaped views; each command chooses how to render its data in those formats.

### Renderer
A function that turns a command's data payload into bytes for a given Format. Each command supplies its own three Renderers (one per Format), and a shared dispatcher in `internal/output` picks the right one based on the resolved Format. The dispatcher owns the "unsupported format" error path so each command stops re-implementing it.
