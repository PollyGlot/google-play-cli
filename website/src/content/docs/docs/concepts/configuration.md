---
title: Configuration
description: gplay's cascading configuration — global config, committed project pins, gitignored local overrides, environment variables, and CLI flags.
sidebar:
  order: 2
---

gplay reads the same `config.json` schema at several levels. Later layers
win:

```txt
$XDG_CONFIG_HOME/gplay/config.json    global, machine-local — Accounts live here
<repo>/.gplay/config.json             project, committed — the package pin
<repo>/.gplay/config.local.json       project, gitignored — per-developer overrides
GPLAY_* env vars                      e.g. GPLAY_ACCOUNT, GPLAY_SERVICE_ACCOUNT
CLI flags                             e.g. --account, --package, --service-account
```

On Linux the global config lives under `$XDG_CONFIG_HOME/gplay` (default
`~/.config/gplay`); on macOS and Windows it is `~/.gplay`, next to other
dotfile-style developer tooling.

## Project pinning

```sh
gplay init --package com.example.myapp
```

writes `.gplay/config.json` at the repo root. Any gplay command run inside
that tree (gplay walks up from the current directory looking for `.gplay/`)
targets that package by default, so `--package` becomes optional. An
explicit `--package` always overrides the pin.

The walk-up refuses to traverse into `$HOME` or any of its ancestors, so a
stray `~/.gplay/config.json` can never masquerade as a project pin — and
`gplay init` refuses to run when the current directory *is* `$HOME`.

## What goes in `.gplay/`

| File | Commit? | Purpose |
| --- | --- | --- |
| `config.json` | **Yes** | The package pin shared with the team |
| `config.local.json` | No (gitignored) | Per-developer overrides, e.g. `account` |
| `edit-<package>.json` | No (gitignored) | Transient open-Edit state |

`gplay init` writes a `.gplay/.gitignore` for you so the local and transient
files stay out of git.

## One hard rule: `account` is never committed

Account names are machine-local. Pinning one in the shared `config.json`
would break every teammate, so the loader **rejects** it with an error
naming the offending file. Put it in `config.local.json`, the
`GPLAY_ACCOUNT` env var, or the `--account` flag instead.

## Environment variables

| Variable | Purpose |
| --- | --- |
| `GPLAY_SERVICE_ACCOUNT` | Service-account credential — a file path or inline JSON. The right choice in CI. |
| `GPLAY_ACCOUNT` | Name of a stored Account to use. |
| `GPLAY_READONLY` | When truthy (`1`/`true`/`yes`/`on`), refuse every command that mutates Google Play state (exit `4`) before any network call. Read commands and `--dry-run` still run. See [gplay for AI agents](/docs/agents/agent-guide/). |
| `GPLAY_INSTALL_NO_VERIFY` | Read by the install script only: bypass the SHA-256 checksum verification (air-gapped / mirrored installs). Prints a warning. See [installation](/docs/getting-started/installation/). |
| `CI` | When `true`, output defaults to JSON. See [output formats](/docs/concepts/output-formats/). |
| `NO_COLOR` | Disable colour in output. |

## Related

- [Authentication & accounts](/docs/concepts/authentication/)
- [`gplay init` reference](/docs/reference/init/)
