# ADR-0004 — Cascading config

## Status

Accepted.

## Context

Once a developer has run `gplay auth login`, every subsequent command
needs to know which Android package to target. Naïvely this means
passing `--package com.example.myapp` on every invocation — fine for a
script, painful in an interactive shell, and a constant source of typos.

`fastlane` solved this with a project-local `Appfile`. We want the same
ergonomics, plus three extra constraints we kept hitting in practice:

1. **Walk-up from cwd.** A developer running `gplay reviews list` from
   `<repo>/apps/android/feature/X` should not have to first `cd` to the
   repo root. Git's `.git/` discovery is the prior art everyone already
   has muscle memory for.
2. **Some settings are machine-local; some are committed.** The package
   name is shared across the team. The Account name (`gplay auth login`
   gives each developer's machine its own Account record) is not. We
   need two layers: one for committed state, one for machine-local
   overrides.
3. **A rogue file in `$HOME` must not hijack every repo.** If walk-up
   blindly followed parents to `/`, a stray `~/.gplay/config.json` would
   masquerade as a project pin from any cwd on the machine.

## Decision

A single filename — `config.json` — appears at three locations, each
serving a distinct purpose:

| Layer | Path | Committed? | Holds |
|---|---|---|---|
| Global | `$XDG_CONFIG_HOME/gplay/config.json` (Linux) or `~/.gplay/config.json` (macOS/Win) | No (machine-local) | Accounts list + active flag, future per-Account registries |
| Project shared | `<repo>/.gplay/config.json` | **Yes** | Package pin (`package`) only |
| Project local | `<repo>/.gplay/config.local.json` | No (gitignored) | Per-developer overrides: `account`, optionally `package` |

Plus two non-file layers that override everything: `GPLAY_*` env vars
and CLI flags.

### Merge order

Later wins:

```
global → project shared → project local → GPLAY_* env → CLI flags
```

### Walk-up rules

- From cwd (inclusive), walk up looking for a `.gplay/` directory.
- Refuse to consider a `.gplay/` whose containing directory is `$HOME`
  **or any ancestor of `$HOME`**. This blocks a rogue `~/.gplay/` from
  becoming a project pin.
- `gplay init` refuses to run when `cwd == $HOME` for the same reason:
  it would create the very file the loader is designed to ignore.

### Field-level rules

- The committed layer (project-shared `config.json`) is **forbidden**
  from containing an `account` field. Account names are machine-local
  identifiers; pinning one in committed state breaks teammates. The
  loader rejects this at parse time with an error pointing to the
  offending file path.
- `config.local.json` and `GPLAY_ACCOUNT` are the supported ways to set
  the active Account for a single repo without touching the user-wide
  global.

### `Resolved` shape

The loader returns a single `*Resolved` value to every command. Fields:

- `Pin` — package name from the cascade.
- `ConfigAccount` — active account name, after project-local overrides
  the global `active` flag.
- `Accounts` — the Accounts list from the global layer (for
  `gplay auth list`).
- `GlobalPath`, `ProjectSharedPath`, `ProjectLocalPath` — for
  diagnostics and writes-back to a specific layer (e.g. `apps add`).

### Where flag/env overrides actually live

The cascade conceptually includes `GPLAY_*` env and flags as the
top-most layers. In practice, **flag/env handling for account names
lives in `internal/auth/resolver`** because it must interleave with the
`--service-account` / `GPLAY_SERVICE_ACCOUNT` direct-credential layers.
Embedding the resolution in `Resolved.ConfigAccount` would force the
resolver to second-guess us; we don't.

## Consequences

- Two co-evolving files share the same JSON schema (with optional
  fields per layer). Tooling that wants to round-trip a layer can do so
  without a layer-specific parser.
- The walk-up adds an extra syscall stack on every command invocation.
  Acceptable: `os.Stat` is microseconds, the user-perceived bottleneck
  is the network round-trip.
- `gplay init` writes a sibling `.gplay/.gitignore` that excludes
  `config.local.json` and `edit-*.json`. New developers cloning the
  repo never have to remember to gitignore those manually.
- The "no `account` in committed config" rule means CI scripts can rely
  on `GPLAY_ACCOUNT` to override without worrying about a committed pin
  fighting them.

## Alternatives considered

- **Single `project.json` like the pre-cascade design.** Rejected
  because we need a per-developer override layer for machine-local
  Account names. A single committed file forces every developer to
  either share an Account (breaks `gplay auth login`'s identity model)
  or never commit project state (breaks the team).
- **Separate filenames per layer** (e.g. `gplay.json` for committed,
  `gplay.local.json` for local). Rejected: the symmetry between global
  and project-shared is valuable. Tools that touch one layer can be
  reused on another by changing only the path.
- **Walk up unconditionally to `/`.** Rejected because of the
  `~/.gplay` hijack concern. The cost of refusing to traverse `$HOME`
  is negligible (most repos sit below `$HOME`); the benefit is a
  predictable model where "I never pinned this package" is the same
  as "no walk-up match".
