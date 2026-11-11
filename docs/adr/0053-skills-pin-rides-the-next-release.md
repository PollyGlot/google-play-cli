# ADR-0053: the install-skills pin rides the next release; fresh skills come from `npx skills add`

## Status

accepted (2026-11-11), amends [ADR-0045](./0045-install-skills-pinned-git-install.md) (Consequences only; its §1 to §5 stand)

## Context

[ADR-0045](./0045-install-skills-pinned-git-install.md) bakes the skills commit
into the binary, so every pack update is a gplay PR that moves
`commands/installskills/skills-pin.json`. That PR can only come after the
release it documents: the skills are rewritten from the published release, and
the skills repo's CI checks them against the latest published binary.

The repin was typed `fix(install-skills)`, because it changes the shipped
binary. release-please reads the type alone, so a repin landing after the tag
opened a new release that held nothing else: 2.4.1 shipped the 2.4.0 repin
(#756), and #765 (the 2.4.1 repin) opened 2.4.2 on its own. Every release
dragged a pin-only patch release behind it.

Two ways out were weighed and rejected:

- **Repin before the release merges**, gated by a required check on the release
  PR. It needs the skills CI to build gplay from an unreleased commit, a second
  ruleset the maintainer cannot bypass, and a marker recording how far the
  skills were reviewed. That is a lot of machinery to keep one channel in step.
- **Drop the pin and install from a tag per gplay version.** It supersedes the
  core of ADR-0045 and moves the integrity anchor out of the binary.

Meanwhile the skills repo already documents `npx skills add
PollyGlot/google-play-cli-skills` as its install, from the tip of `main`. That
channel is fresh minutes after a skills PR merges, needs no gplay release, and
is the user's own choice of tool: gplay executes nothing.

## Decision

### 1. Two channels, named for what each guarantees

- `npx skills add PollyGlot/google-play-cli-skills` installs the current skills,
  from the skills repo's `main`. It needs Node, and is the way to stay current.
- `gplay install-skills` installs the pack pinned in the binary, with git only.
  It is reproducible per gplay version and can lag one release behind.

The README and the site present both. The `--help` of `install-skills` states
the lag and points to the skills repo for the current install, without naming
`npx`: the installer source stays free of package-runner names, a guard its
tests enforce. gplay itself never runs `npx` (ADR-0045 §1 stands).

### 2. The repin is a `chore`, made by a script

`scripts/repin-skills.sh <skills-commit>` moves the pin and commits it as
`chore(install-skills): pin the skills for X.Y.Z`. release-please hides `chore`
and does not bump on it, so the repin ships with the next release instead of
cutting one. The script refuses a commit that is not on the skills repo's
`main`, and reads the skill list back from that commit.

This is the one exception to the rule that a change to the shipped binary is a
`feat` or a `fix`: the pin is content the binary fetches, not behaviour, and the
fresh copy is already one `npx` away.

### 3. The release agent does the whole sync

After each release, the release-announce agent updates the skills, merges the
skills PR once its CI is green against the new binary, runs
`scripts/repin-skills.sh` on the merged commit, and merges the repin through
`scripts/merge-pr.sh`. No human step remains between a gplay release and fresh
skills.

## Consequences

- No release exists only to move the pin.
- `npx` users get skills matched to a release minutes after it ships.
- The binary `vX.Y.Z` embeds the pack written for the release before it. A user
  who stays on `gplay install-skills` gets the newer pack with the next gplay
  upgrade, as ADR-0045 already accepted.
- The skills PR is merged by an agent, not reviewed by a human. Its guard is
  the skills repo's required `check` against the published binary.

## Considered options

- **Repin before the release merges, behind a required check.** Rejected for
  the machinery listed above.
- **Install from an immutable tag per gplay version.** Parked: it would keep
  `install-skills` current too, at the cost of a resolver in the binary and a
  new trust anchor. Revisit if the one-release lag hurts in practice.
- **Shell out to `npx skills add` again.** Rejected for ADR-0045's reasons:
  gplay would run an unpinned third-party program and require Node.
