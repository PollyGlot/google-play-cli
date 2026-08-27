---
title: Agent skills
description: "Pre-built agent skills for gplay: one per command namespace, installed with gplay install-skills, for Claude Code and compatible AI coding tools."
sidebar:
  order: 2
---

**Agent skills** drive gplay from natural-language prompts. Each skill is a
folder with a `SKILL.md` that documents the intent, the gplay commands it
runs, and the safety rails it enforces. They live in a companion repository:
[PollyGlot/google-play-cli-skills](https://github.com/PollyGlot/google-play-cli-skills).

## Install

```sh
gplay install-skills
```

The skills are fetched with `git` from a commit pinned inside the gplay
binary, so two runs of the same version install the same reviewed files.
Nothing else is executed: no Node, no package runner, no script from the
skills repository. `git` is the only requirement.

They land in `~/.claude/skills`, user-wide, which Claude Code and the agent
frameworks following that layout read. Use `--dir` to install elsewhere. Only
the skills listed below are replaced; anything else in that directory is left
alone, and a failed install is rolled back.

## The roster

One skill per workflow, plus a foundation skill for the conventions they all
share:

| Skill | Drives |
| --- | --- |
| `gplay-cli-usage` | Credential and package resolution, output, exit codes, safety gates, the Edit lifecycle (foundation) |
| `gplay-setup` | Auth onboarding: service account, login, doctor |
| `gplay-apps` | App registry, reachable apps, app details |
| `gplay-release-flow` | Upload, promote, staged rollouts, mappings, Internal App Sharing |
| `gplay-tracks` | Tracks, closed-track testers, country availability |
| `gplay-reviews` | Review triage, replies, the monthly CSV history |
| `gplay-metadata-sync` | Store listing text and images |
| `gplay-compliance` | Data Safety declarations |
| `gplay-team` | Users, grants, permission vocabulary |
| `gplay-monetization` | Subscriptions and one-time products as declarative files |
| `gplay-orders` | Order lookup and refunds |
| `gplay-vitals` | Crash/ANR rates, error reports, Play-detected anomalies |
| `gplay-games` | Achievements and leaderboards configuration |
| `gplay-recovery` | App recovery actions when a shipped build is broken |
| `gplay-device-tiers` | Device tier configs for tiered asset delivery |
| `gplay-customapps` | Private app creation for managed Google Play |
| `gplay-appstore` | Alternative app store: catalog, update feed, hosted-app review |

## Why skills instead of raw prompting?

A skill encodes the **safe path** for a workflow: which command sequence,
which rehearsal steps (`--dry-run` first), which acknowledgment flags exist
and when a human should approve them. The
[agent contract](/docs/agents/agent-guide/) explains the CLI-level
primitives the skills build on.
