---
title: gplay for AI agents
description: Why gplay is agent-first — machine-stable JSON, semantic exit codes with resolvable refusals, dry-runs that announce required safety flags, offline API introspection, and llms.txt.
sidebar:
  order: 1
---

gplay is designed to be **driven by AI agents** — coding assistants, CI
bots, autonomous release managers — as a first-class use case, not an
afterthought. This page is the contract an agent can rely on.

## Machine-stable output

- Piped output (and `CI=true`) defaults to **JSON** — an agent running gplay
  through a shell tool gets JSON without asking.
- For commands that wrap a Play API call, JSON is the **API's own response
  shape** ([pass-through](/docs/concepts/output-formats/)) — schemas are
  documented by Google, not invented by gplay.
- stdout carries data only; logs and warnings go to stderr. Parsing stdout
  never breaks on a log line.
- `--output markdown` renders tables and checklists ready to paste into a
  PR comment or chat reply.

## Semantic exit codes

The [exit code taxonomy](/docs/concepts/exit-codes/) makes failure handling
decidable without parsing prose: `40`/`50` are retry-safe, `2` means the
command was malformed, `10`/`11` mean credentials/permissions, `60` means
state conflict.

**Exit `3` is the agent-resolvable refusal**: the command was well-formed,
but a named safety acknowledgment (`--confirm`, `--grant-admin`) is
missing — and the error names it. An agent can surface the decision to a
human, or re-run with the flag if its policy allows.

Under `--output json`, a failure also writes a structured envelope to stdout
(`{"error":{"exitCode","message","reasons","requires"}}`), so the failure is
parseable without scraping stderr — and `requires` names the missing flag at
failure time, not just in a dry-run. See
[output formats](/docs/concepts/output-formats/).

## The read-only authority boundary — `GPLAY_READONLY`

`--confirm` and `--grant-admin` are *advisory*: an agent that holds the
credential can pass them itself. `GPLAY_READONLY` is the boundary a **harness**
imposes regardless of what the model chooses. When it is truthy
(`1`/`true`/`yes`/`on`), every command that mutates Google Play state is
refused with **exit 4** — *before* credential resolution and any network
call — while read commands and `--dry-run` of mutating commands still run. So
an agent can observe and plan against a production credential, but cannot
publish unless the environment lets it.

Exit 4 is deliberately distinct from exit 3: it is **not** resolvable by
adding a flag (the message says so). Pair it with a read-only service account
for defence in depth — see the [CI/CD guide](/docs/guides/ci-cd/).

## Bounded runtime — `--timeout` and `--retry`

Two global flags make unattended runs predictable. `--timeout <duration>`
bounds each API request (control-plane calls default to 60s; uploads are
exempt unless you set it). `--retry N` retries the transient classes
(transport errors, 5xx, 429 honouring `Retry-After`) with exponential
backoff, and never retries non-transient 4xx or `edits.commit`. Both are safe
defaults for an agent to set on every call.

## Rehearse before writing

Every write command accepts `--dry-run`: validate inputs and preview the
payload with **no HTTP call**. The team write commands go further — their
`--dry-run --output json` includes a `requires` array naming the safety
flags the live write would need:

```sh
gplay team users add dev@example.com --role admin --dry-run --output json
# → { ..., "requires": ["grant-admin"] }
```

So an agent can discover the gate *before* tripping it.

## No prompts, ever

gplay never asks an interactive question. Required acknowledgments are
expressed as flags (exit `3` when missing), credentials come from flags,
env vars, or the stored Account — nothing blocks on a TTY.

## Offline API introspection

`gplay schema` queries an embedded index of the Android Publisher API —
methods, request/response types, enums — with **no network and no
credentials**:

```sh
gplay schema edits.tracks.update
gplay schema Track
gplay schema --list --method POST
```

An agent can answer "what fields does a Track carry?" without leaving the
shell. (`[experimental]` — the JSON shape may still evolve.)

## llms.txt

This site publishes [/llms.txt](/llms.txt) and a concatenated
[/llms-full.txt](/llms-full.txt) so agents can ingest the documentation —
including the full generated command reference — in one fetch.

## Ready-made skills

If your agent framework supports skills (Claude Code and compatible tools),
the [companion skills](/docs/agents/skills/) package these
conventions per workflow — install with
`npx skills add PollyGlot/google-play-cli-skills`.
