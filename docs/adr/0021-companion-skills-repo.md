# Companion skills repo: separate sibling, comprehensive area-aligned roster, `--help`-first SKILL.md contract

## Status

accepted

## Context

gplay's v1 story is agent-first (CLAUDE.md; [ADR-0019](0019-canonical-verb-vocabulary.md)).
The artifact that lets an arbitrary AI agent actually drive gplay is a set of
**Agent Skills** — `SKILL.md` folders an agent loads on demand. That set is
[#53](https://github.com/PollyGlot/google-play-cli/issues/53).

Three things make now the right time and shape this decision:

- **The verb gate is closed.** ADR-0019 named *this* skills repo as the live
  trigger for freezing the verb vocabulary — a published `SKILL.md` that drives
  `gplay tracks status` is as expensive to rename as a frozen public contract
  once agents depend on it. The renames (#163–168) and the verb audit (#98) are
  merged, so skills can pin to final names from birth.
- **The shipped surface is broad, not the old "strict MVP."** auth, apps,
  releases, tracks, reviews, metadata, compliance, team, testers are all live and
  writable today. Only vitals ([#49](https://github.com/PollyGlot/google-play-cli/issues/49))
  and subscriptions ([#51](https://github.com/PollyGlot/google-play-cli/issues/51))
  remain ungated.
- **The packaging is a solved shape.** A companion skills repo installed via
  `npx skills add`, built around a foundational CLI-usage skill plus per-area
  workflow skills, is an established layout.

This ADR settles: where skills live, how many and along what axis, what a
`SKILL.md` must contain, how skills stay correct as the CLI evolves, and how they
relate to the dormant 1.0 freeze.

## Decision

### 1. Separate sibling repo, installed via `npx skills add`

`PollyGlot/google-play-cli-skills` — **not** a `skills/` directory in this repo.
Reasons: (a) the install contract is `npx skills add <org>/<repo>`, so the repo
*is* the unit; (b) skills version on their own cadence — they track the CLI's
*observable behavior*, not its source tree; (c) it matches the established
companion-repo layout; (d) it keeps the Go module clean. Layout: one folder per
skill, each with a `SKILL.md` plus optional `scripts/` / `references/`. License
MIT, matching this repo.

### 2. Comprehensive, area-aligned roster — not the minimal "GA-4"

One skill per coherent namespace/workflow, covering the **entire shipped
surface**, plus one foundation skill. Nine skills:

| Skill | Covers |
|---|---|
| `gplay-cli-usage` | foundation: auth precedence, `--package`/project pinning, output `table`/`json`/`markdown`, exit codes, `--dry-run`/`--confirm`, `--help`-first discovery, the `Edit` lifecycle |
| `gplay-setup` | `auth login` / `status` / `doctor` / `list` / `logout`, then hand off to `gplay-apps` |
| `gplay-apps` | `apps add` / `list` / `view` / `remove` / `init` + `apps details view` / `set` |
| `gplay-release-flow` | `releases upload` / `promote` / `rollout` (+ `halt` / `resume` / `complete`) / `list` / `trackhint` |
| `gplay-tracks` | `tracks list` / `view` / `create` + `tracks availability view` + `testers list` / `set` |
| `gplay-reviews` | `reviews list` / `reply` |
| `gplay-metadata-sync` | `metadata list` / `pull` / `validate` / `apply` + `metadata images …` |
| `gplay-compliance` | `compliance datasafety set` / `validate` |
| `gplay-team` | `team users …` + `team grants …` + `team permissions` |

The minimal "v1 GA = 4 skills" framing from #98 is **superseded**: we ship
coverage of everything that exists. A gated area gets its skill when its commands
land (vitals, subscriptions). There is deliberately **no `gplay-id-resolver`
analogue** — package names and the local apps registry are already human-legible
handles, so an ID-resolution skill has no gplay counterpart; a place where
gplay's addressing design erases an entire skill.

**Granularity rule:** *skill := one namespace*, unless two namespaces are
meaningless apart — testers exist only for closed tracks (DESIGN §10 couples
"Tracks and testers"), so they fold into `gplay-tracks`. Finer workflow
splits (a dedicated whats-new-writer, an ASO audit, a localization skill) are
**future additive** skills, not a v1 cut.

### 3. `--help`-first SKILL.md contract — no frozen flag lists

Frontmatter is `name` + `description` (the `description` carries the "Use when …"
triggers); the body is Markdown. The body teaches the *workflow and its decision
points* and instructs the agent to confirm the live interface with
`gplay <cmd> --help` rather than hard-coding exhaustive flag lists that drift
(the AGENTS.md "always check `--help`" convention). Cross-cutting conventions
(output, exit codes, auth, dry-run/confirm, the `Edit` lifecycle) live **once** in
`gplay-cli-usage`; workflow skills reference it instead of repeating it.

### 4. Decoupled from the dormant 1.0 freeze

Skills ship **now, in 0.x**. The "CLI 1.0 + skills v1 ship together" coupling from
#98 is superseded by the 0.x-rolling delivery decision (ROADMAP). The verb names
skills pin to are frozen by ADR-0019 independently of the 1.0 milestone, so there
is no contract risk in shipping skills before any 1.0 gel.

### 5. Anti-drift is editorial — the skills repo carries no CI

This section originally specified a CI guard: a `SKILL.md` frontmatter lint plus
a command-drift gate (every `gplay …` string cited in a skill must resolve to a
**real** command, which needs a `gplay` binary in the skills-repo CI). That is
**reversed** — the companion repo is **docs-only and runs no CI**.

Drift is prevented editorially instead: every skill is authored `--help`-first
against the live binary and pinned to the ADR-0019 vocabulary, and that is
checked at review time. A frontmatter lint was bootstrapped with the repo
(slice #183) and then removed; the command-drift gate (#184) was closed
won't-do. The rationale: a hand-curated docs repo of nine files does not earn a
CI pipeline (nor a `gplay` binary inside it), and `--help`-first authoring plus
the frozen ADR-0019 names already give the same protection. The frontmatter
contract from §3 survives as README authoring guidance, enforced by review —
not a gate.

## Consequences

- `/to-issues` decomposes #53 into a tracer-bullet first slice (bootstrap repo +
  README/install + `gplay-release-flow`), then one slice per remaining skill,
  filed on `PollyGlot/google-play-cli` (slice 1 *is* the bootstrap). The
  frontmatter-lint that shipped with the bootstrap and the separate drift-gate
  slice (#184) were both dropped per §5 — the repo is docs-only.
- `CLAUDE.md` "Skills structure" and `AGENTS.md` "gplay skills" are corrected to
  the canonical names (`gplay-track-management` → `gplay-tracks`; the speculative
  `gplay-subscription-management` stays gated) and expanded to the 9-skill roster.
- `ROADMAP.md` stops listing #53 under Parking.
- "Skill" / "companion skills repo" are tooling/meta terms, not Play-domain nouns,
  so they do **not** enter `CONTEXT.md` — the glossary stays domain-only.
- The skills repo runs **no CI**, so there is no `gplay` binary in any
  skills-repo pipeline. What keeps a "published skill" durable instead is review
  discipline (§5): `--help`-first authoring against the live binary plus the
  frozen ADR-0019 names, checked when a skill change is reviewed.

## Considered options

- **Monorepo `skills/` directory.** Rejected: breaks the `npx skills add
  <org>/<repo>` unit, couples skill cadence to CLI releases, pollutes the Go
  module.
- **Minimal "GA-4" only** (issue 53 / #98). Rejected per the product call: cover
  the whole shipped surface now; the 4 are a subset of the 9.
- **A fine-grained split** (several skills per area). Deferred: the
  right altitude for v1 is one-per-namespace; finer workflow skills are additive
  later, not a launch requirement.
- **Freeze flag lists inside each `SKILL.md`.** Rejected: they drift; `--help` is
  the live source, and command *names* stay honest via `--help`-first authoring
  and the ADR-0019 freeze (no drift-gate CI — see §5).
- **A CI drift gate + frontmatter lint** (§5 as first written). Adopted, then
  reversed: a frontmatter lint shipped with the bootstrap and was removed, and
  the drift gate (#184) was closed won't-do. A docs-only repo of nine
  hand-written files does not justify a CI pipeline (or a `gplay` binary in it);
  review plus `--help`-first authoring covers it. See §5.
- **Ship a `gplay install-skills` CLI command.**
  Parked: it is a CLI feature in *this* repo, separate from bootstrapping the
  skills repo; `npx skills add` suffices now. **Later adopted** in
  [ADR-0028](0028-install-skills-command.md): field feedback showed agents do not
  discover `npx skills add` unprompted, invalidating the "suffices now" premise.
- **Wait for 1.0.** Rejected — see §4.
