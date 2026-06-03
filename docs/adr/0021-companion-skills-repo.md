# A companion skills repo: a separate, docs-only `SKILL.md` repo pinned to gplay's live `--help` surface

## Status

accepted

## Context

gplay's v1 story is agent-first + CI + manual (the lens that decided
[ADR-0019](0019-canonical-verb-vocabulary.md)). The agent half is carried by
**skills** — `SKILL.md` instruction files that teach an AI agent how to drive
gplay at the prompt. Two questions had to be settled before writing the first
one: *where do skills live*, and *what shape does a `SKILL.md` take* so that a
fleet of them stays honest as the CLI evolves.

The trigger was PRD #53 ("Skills compagnons — repo + set v1 GA"), decomposed
into a bootstrap slice (#183) plus one slice per surface (#185–#192) and an
anti-drift slice (#184). ADR-0019 already named this repo as the reason to
freeze the verb vocabulary *now*: a published `SKILL.md` that drives `gplay
tracks view` is as expensive to rename as a frozen public contract once agents
depend on it. So the skills' contract **is** the CLI's command surface, and
the two are coupled by design.

The install vector is `npx skills add <user>/<repo>`, which wants a repository
whose root is a tree of skills. The sibling CLI **asc** (App Store Connect)
ships companion skills the same way (`asc-release-flow`, `asc-metadata-sync`,
…), and was used as the precedent for both topology and the `SKILL.md` shape.

## Decision

### 1. A separate sibling repo, not a monorepo subdirectory

Skills live in **`PollyGlot/google-play-cli-skills`** — public, MIT, layout
*one folder per skill* (`skills/<name>/SKILL.md`), installed with `npx skills
add PollyGlot/google-play-cli-skills`. It is a **sibling** of the CLI repo, not
a subdirectory of it:

- skill prose iterates at a different cadence than the Go code, and a separate
  repo keeps each history clean;
- a skill consumer should not need the Go toolchain (or the CLI repo's CI) to
  install instructions;
- `npx skills add` wants a repo root of skills, which a monorepo subdir is not.

### 2. The roster mirrors the GA surface

A **foundation** skill `gplay-cli-usage` encodes the cross-cutting conventions
once (auth/account resolution, `--package` pinning, output formats, exit codes,
`--dry-run`/`--confirm`, the implicit Edit lifecycle); every workflow skill
references it instead of repeating them. The workflow skills are one per
surface: `gplay-setup`, `gplay-apps`, `gplay-release-flow`, `gplay-tracks`,
`gplay-reviews`, `gplay-metadata-sync`, `gplay-compliance`, `gplay-team`.
Surfaces whose CLI has not shipped stay **gated**: vitals (#49),
subscriptions & IAP (#51). The roster grows by adding a folder and a catalogue
row when a surface lands.

### 3. The `SKILL.md` contract

Every `SKILL.md` opens with YAML frontmatter carrying exactly two required
fields:

- **`name`** — kebab-case, identical to the skill's folder name.
- **`description`** — one line ending in a **"Use when …"** trigger clause, so
  an agent can decide relevance; the same text is the skill's row in the README
  catalogue's "Use when" column.

The body is **`--help`-first**: it pins command *shapes*, mental models, and
the gotchas `--help` alone won't state — and deliberately **does not freeze
flag lists**, which drift. `gplay <cmd> --help` is the source of truth; the
skill says so. Verbs are pinned **post-ADR-0019** (no condemned name ever
appears — e.g. `tracks view` not `tracks status`, `team grants remove` not
`revoke`). Workflow skills stay **DRY** by referencing `gplay-cli-usage` for
the shared conventions rather than restating them.

### 4. Anti-drift is editorial, not CI

The skills repo is **docs-only and carries no CI**. Drift between a `SKILL.md`
and the CLI is prevented at authoring and review time: each skill is written
`--help`-first against the live binary and pinned to the ADR-0019 vocabulary.
A frontmatter linter (#183) and a "cited commands exist" guard (#184) were
considered — the linter was even shipped first — and then **dropped**: a
hand-curated docs repo of a handful of files does not earn a CI pipeline, and
the same protection already comes from writing against `--help` and from
ADR-0019's frozen names. The frontmatter contract from §3 stays documented in
the README as authoring guidance, enforced by review.

## Consequences

- A published `SKILL.md` is part of gplay's public contract: renaming a command
  breaks the agents that depend on it — exactly the cost ADR-0019 pays down.
  This repo is what makes that vocabulary load-bearing.
- No CI means **review discipline**: a malformed frontmatter or a stale command
  name is caught by a human or agent reading the diff, not by a gate. That is an
  accepted trade for a small docs repo and is revisited only if the roster grows
  past what review can cover.
- The CLI and the skills version **independently**; the skills track gplay's GA
  surface, not its internal version number.
- `gplay-cli-usage` is a load-bearing dependency of every other skill: a change
  to a cross-cutting convention is edited there once.

## Considered options

- **Monorepo — skills under `google-play-cli/skills/`.** Rejected: couples
  skill iteration to CLI releases, imposes the Go toolchain on skill consumers,
  and is not a clean `npx skills add` root.
- **Keep the CI (frontmatter lint #183 + anti-drift guard #184).** Shipped, then
  reversed: a docs-only repo of nine hand-written files does not justify a CI
  pipeline; `--help`-first authoring plus the ADR-0019 freeze give the same
  drift protection. The frontmatter rules survive as README guidance, not a gate.
- **One mega-skill instead of one-per-surface.** Rejected: agent tool-selection
  degrades with broad, vague skills; a sharp per-surface `description` ("Use
  when …") plus a DRY foundation skill selects better than a single catch-all.
- **One repo per skill.** Rejected: too granular for `npx skills add`; a single
  repo with a catalogue is the right install unit.
