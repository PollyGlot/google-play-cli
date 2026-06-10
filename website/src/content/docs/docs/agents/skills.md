---
title: Agent skills
description: Pre-built agent skills for gplay — one per command namespace, installable with npx skills add, for Claude Code and compatible AI coding tools.
sidebar:
  order: 2
---

**Agent skills** drive gplay from natural-language prompts. Each skill is a
folder with a `SKILL.md` that documents the intent, the gplay commands it
runs, and the safety rails it enforces. They live in a companion repository:
[PollyGlot/google-play-cli-skills](https://github.com/PollyGlot/google-play-cli-skills).

## Install

```sh
npx skills add PollyGlot/google-play-cli-skills
```

Works with Claude Code and other agent frameworks that read the `skills`
format.

## The roster

One skill per shipped namespace, plus a foundation skill for cross-cutting
conventions:

| Skill | Drives |
| --- | --- |
| `gplay-cli-usage` | Cross-cutting conventions: output, exit codes, auth resolution (foundation) |
| `gplay-setup` | Auth onboarding — service account, login, doctor |
| `gplay-apps` | Apps registry + app details |
| `gplay-release-flow` | upload / promote / staged rollouts |
| `gplay-tracks` | Tracks + closed-track testers |
| `gplay-reviews` | Review triage and replies |
| `gplay-metadata-sync` | Store listings + images sync |
| `gplay-compliance` | Data Safety declarations |
| `gplay-team` | Users, grants, permission vocabulary |

Two more are gated until their CLI surfaces ship: `gplay-vitals`
([#49](https://github.com/PollyGlot/google-play-cli/issues/49)) and
`gplay-subscription-management`
([#51](https://github.com/PollyGlot/google-play-cli/issues/51)).

## Why skills instead of raw prompting?

A skill encodes the **safe path** for a workflow: which command sequence,
which rehearsal steps (`--dry-run` first), which acknowledgment flags exist
and when a human should approve them. The
[agent contract](/docs/agents/agent-guide/) explains the CLI-level
primitives the skills build on.
