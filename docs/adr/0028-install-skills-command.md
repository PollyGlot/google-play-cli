# `gplay install-skills`: a thin, opt-in installer for the companion skills — accepting Node/npx for one meta-command

## Status

accepted (un-parks the option deferred in [ADR-0021](0021-companion-skills-repo.md))

## Context

[ADR-0021](0021-companion-skills-repo.md) shipped the companion skills repo
(`PollyGlot/google-play-cli-skills`) and **parked** a `gplay install-skills` CLI
command on the reasoning that "`npx skills add` suffices now." Field feedback
invalidated that premise: a gplay user reported that their AI agent, told to
"install gplay," installed the binary but never discovered the skills existed —
the user had to read the README and explicitly instruct the agent to run
`npx skills add`. The thing that failed is exactly the parked option's premise —
that discoverability was already solved.

A CLI-owned installer is a proven shape for this problem. This ADR un-parks it,
settles its behavior and defaults, reconciles it with gplay's "no Node"
identity, and records why discoverability is surfaced passively rather than via
a per-invocation nudge.

## Decision

### 1. Ship `gplay install-skills` — flat, category-3 meta-command

The command is `gplay install-skills`: a flat, hyphenated meta-command in the
**reference / diagnostic / scaffold** category ([ADR-0019](0019-canonical-verb-vocabulary.md) §3 /
DESIGN §0), alongside `version`, `exit-codes`, `init`. Precedent for a hyphenated
flat meta-command is `exit-codes`.

It is **not** a `skills` namespace (`gplay skills install`). A namespace would
elevate *skills* to a first-class resource alongside `apps`/`tracks`/`releases`,
contradicting ADR-0021's ruling that "Skill"/"companion skills repo" are
tooling/meta terms, not Play-domain nouns. The category-3 slot exists precisely
for commands outside the resource grammar.

### 2. Thin wrapper over `npx skills add`, opinionated non-interactive defaults

Bare `gplay install-skills` runs:

```
npx --yes skills add PollyGlot/google-play-cli-skills --global --agent '*' --yes
```

- **`--global`** — skills drive the *binary*, installed user-wide, so they are
  useful in every project, not just the current one. Without `-g`, the skills
  CLI's `-y` auto-detects "project" inside a repo — the wrong scope.
- **`--agent '*'`** — all detected agents. gplay is agent-agnostic (`CLAUDE.md`
  *and* `AGENTS.md` exist; positioning is "driven by AI agents," not one vendor).
  Pinning the install to a single agent vendor is deliberately avoided.
- **`--yes` (twice)** — non-interactive, required by DESIGN's
  no-interactive-prompts rule; an interactive `npx` prompt would break agent and
  CI invocations.

Extra args pass through to `npx skills add` for overrides (`--project`,
`--agent claude`, …).

### 3. Node/npx is an accepted, opt-in dependency for this command only — the "no Node" pillar holds

The command shells out to `npx`, so it needs Node. This is reconciled with the
marketed pillar ("Standalone Go binary. No Ruby, no Node, no Python — one file in
your CI image") as follows:

- The pillar is scoped to the **CI/runtime path** — driving the Play API needs
  zero runtime dependencies, and that stays true.
- `install-skills` is a **dev-workstation setup convenience**, never on the CI
  path.
- Node was **already required** to install skills: `npx skills add` is *the*
  install contract (ADR-0021). The command introduces no new dependency; it wraps
  the one that already existed.

The README's "no Node" claim is therefore left **unchanged**. Honesty is kept at
the point of use: the command's `--help` states plainly that it requires
Node/npx. Note [ADR-0007](0007-raw-http-not-google-go-sdk.md) does not cover this
— it scopes itself to the API surface, not runtime tooling; the relevant claim is
the README/POSITIONING "no Node" pillar, addressed above.

### 4. npx-absent → exit 1 with the manual recipe printed

If `npx` is not on `PATH`, exit **1** (the documented generic fallback — no code
in the Play-API-shaped taxonomy fits a missing local dependency) and print an
actionable stderr message that includes the exact fallback command:

```
npx requires Node.js, which was not found.
Install Node.js, then run:
    npx skills add PollyGlot/google-play-cli-skills --global --agent '*' --yes
Or browse the skills: https://github.com/PollyGlot/google-play-cli-skills
```

An agent that hits this leaves with *what to do*, not a dead end. A failed
`npx`/`skills` run also surfaces as exit 1 (npx failures are opaque).

### 5. Surface the command passively — no per-invocation nudge

For the command to solve discoverability, an agent must find it. Three passive
surfaces:

1. Listed in `gplay --help` (root) — idiomatic with the "run `--help` to discover
   the interface" convention.
2. A post-install tip in `install.sh` pointing at `gplay install-skills` — the
   reliable channel for the "install gplay" → agent flow.
3. The README's skills section elevated toward the top.

A per-invocation nudge (shell out on every run to check whether skills are
installed) is **rejected**: it puts `npx`/Node on the hot path of every command,
contradicting ADR-0007 reason #3 (cold start is in the critical path for every CI
invocation), and cross-agent "are skills installed?" detection is fragile. The
three passive surfaces cover the flow without the cold-start cost.

## Consequences

- ADR-0021's parked "Considered options" line gains a pointer to this ADR (not a
  rewrite — ADRs are immutable point-in-time records).
- DESIGN §0 lists `install-skills` under category 3 (reference / diagnostic /
  scaffold).
- verb-gate is unaffected: it bans pre-rename names, it does not validate new
  verbs; `install` is a category-3 meta verb — a reviewed design choice, not a
  gated one.
- Shipped as a single issue
  ([#266](https://github.com/PollyGlot/google-play-cli/issues/266)), prioritized
  ahead of the parked long-tail PRD drafts
  ([#241](https://github.com/PollyGlot/google-play-cli/issues/241) /
  [#242](https://github.com/PollyGlot/google-play-cli/issues/242) /
  [#243](https://github.com/PollyGlot/google-play-cli/issues/243)); not its own
  PRD.

## Considered options

- **A `skills` namespace** (`gplay skills install`, + future `skills
  list`/`update`). Rejected: elevates skills to a resource, contradicting
  ADR-0021's "skills = meta, not domain"; YAGNI (the flat command covers the
  need; no `skills *` family is planned).
- **Hardcode a single agent vendor.** Rejected: gplay is multi-agent;
  `--agent '*'` matches positioning.
- **A per-invocation discoverability nudge.** Rejected on cold-start grounds
  (ADR-0007 #3) and detection fragility; passive surfaces chosen instead.
- **Soften the README "no Node" claim.** Rejected: the claim is CI/runtime-scoped
  and stays true; Node was already required to install skills via npx regardless.
- **Keep it parked** (status quo, "`npx skills add` suffices"). Rejected: real
  user feedback shows agents do not discover `npx skills add` unprompted.
