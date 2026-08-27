# `install-skills` installs from a pinned git commit, verified and reversible, instead of shelling out to a package runner

## Status

accepted (supersedes the installer mechanism of [ADR-0028](0028-install-skills-command.md) §2, §3 and §4; ADR-0028's command shape, category and discoverability decisions stand)

## Context

[ADR-0028](0028-install-skills-command.md) shipped `gplay install-skills` as a
thin wrapper over `npx skills add`. That bought a working installer in a few
lines, and paid for it with a supply chain gplay does not control:

- `npx` resolves and executes the latest published `skills` package at run time.
  A compromised release of that package runs arbitrary code on the workstation
  of every user who types `gplay install-skills`.
- The skills themselves were pulled from a **mutable branch tip**. Two runs of
  the same gplay binary, a week apart, could install different files, and a
  push to the skills repo reached users with no gplay review in between.
- The command executed a third-party program with the user's ambient
  credentials, and gplay could say nothing about what it had installed
  afterwards: no manifest, no verification, no way back.
- It made Node a requirement for a Go binary whose whole pitch is not needing
  one, which had to be argued away in the README, the ADR and the `--help`.

The skills are static Markdown. Nothing about the payload justified a package
runner.

## Decision

### 1. Fetch a pinned commit with `git`, execute nothing else

`install-skills` runs `git init`, `git remote add`, `git fetch --depth 1 origin
<commit>`, `git checkout FETCH_HEAD` in a throwaway directory, then copies
files. No package runner, no npm package, no script from the skills repository
is executed, at any point. `git` is the only runtime requirement, so the "no
Node" pillar now holds for this command too, and ADR-0028 §3's carve-out is
retired.

`git fetch <commit>` is used rather than `git clone`: clone resolves a *ref*,
which whoever controls the remote can rewrite. Asking for an object name makes
the server produce that exact object or fail. `git rev-parse HEAD` is then
compared to the pin, so the checkout is re-read rather than assumed.

### 2. The pin lives in the binary, and only the commit anchors integrity

`commands/installskills/skills-pin.json` is `go:embed`ed and carries the repo
slug, the clone URL, a **full 40-character commit** (an abbreviation is a prefix
match, so it does not pin one tree), the pack subdirectory, and the sorted list
of expected skill names. Bumping the pack is therefore an ordinary reviewed PR
against gplay: `commands/` counts as code, so the full gate runs on it and a
human sees the diff of what users will receive.

No per-file digest table is kept. Git already guarantees the fetched tree hashes
to the pinned commit, so a digest list would restate the same fact in a second
place that has to be regenerated on every bump: two sources of truth for one
guarantee, and the weaker one drifts.

The skill **names** are pinned separately from the commit because they answer a
different question: not "is this the reviewed tree" but "is this the complete
pack". A checkout missing an expected skill, or carrying one the pin does not
list, is refused before anything is written.

### 3. Stage, then swap, then verify, then roll back on any failure

The install is a four-phase sequence, and the target directory is not touched
until the last possible moment:

1. **Stage**: each skill is copied into a temporary directory *inside* the
   target (same filesystem, so every later move is a rename, not a copy) and
   each staged file is verified byte for byte against the checkout.
2. **Swap**: per skill, any existing directory of the same name is renamed into
   a backup directory, then the staged one is renamed into place. The unit of
   replacement is the skill directory: whatever else lives in the target (a
   hand-written skill, another tool's pack) is never touched.
3. **Verify**: every installed file is re-hashed against the checkout, on its
   real installed path. A file that is missing, altered or unexpected fails the
   install.
4. **Roll back**: any failure in phases 2 or 3 walks the completed swaps back in
   reverse, restoring displaced directories and removing newly added ones. If
   the rollback itself is incomplete, that is reported in the same error, not
   swallowed.

Symlinks and other irregular files in the pack are refused rather than followed:
a symlink resolves against the *user's* filesystem at install time.

### 4. One target directory, `--dir` to move it; no passthrough

Skills land in `~/.claude/skills`, the standard agent-skills layout, user-wide
for the reason ADR-0028 §2 gave (skills drive the binary, so they are useful in
every project). `--dir` retargets it for an agent that reads elsewhere.

The npx passthrough is gone with the npx. `install-skills` now takes no
positional arguments, and `--agent` / `--project` no longer exist: they were
flags of a third-party CLI that gplay merely forwarded to. This is a breaking
change to a leaf classified as frozen Public ([ADR-0042](0042-one-zero-ga-and-stability-label-mechanism.md)),
and it ships as one: a stability label is not a promise to keep executing an
unreviewed third-party program.

### 5. Failures stay opaque, exit 1

Unchanged from ADR-0028 §4: a missing `git`, a failed fetch, a pack mismatch and
a failed verification all exit **1**, the documented generic fallback (no
Play-API-shaped code fits a local dependency or a local filesystem problem).
Git's own output is folded into the message; git's exit code (128) is
deliberately *not* propagated, so the error chain is broken with `%v`, never
`%w`, at every git call site.

A missing `git` prints an actionable recipe with the browse URL for the pinned
commit, so an agent that hits it leaves with the files' location rather than a
dead end.

## Consequences

- ADR-0028's §2 recipe, §3 Node carve-out and §4 npx-failure wording are
  historical: the command shape (flat category-3 meta-command), the rejection of
  a `skills` namespace, and the passive-discoverability decisions all stand.
- The README's "no Node" pillar no longer needs a footnote for this command.
- `install.sh`'s post-install tip names `git`, not Node.
- Shipping a new or updated skill now requires a gplay PR bumping the pin, and
  users get it with their next gplay upgrade. That latency is the point: it is
  the review step the branch tip did not have.
- Tests run against a local fixture git repository, so the install path is
  exercised end to end without the network. They require `git` in the test
  environment and skip without it.

## Considered options

- **Keep `npx skills add`.** Rejected: it executes an unpinned third-party
  program on the user's machine to install static Markdown.
- **Pin a tag instead of a commit.** Rejected: a tag is a mutable pointer; the
  remote can move it after review.
- **Embed the skills in the binary with `go:embed`.** Tempting (zero runtime
  dependency, nothing to verify) and the natural next step, but it couples every
  skill edit to a gplay release build and inflates the binary with content most
  users of the CLI never read. Parked, not rejected: the pin file makes the move
  cheap later, since the expected pack is already declared.
- **Ship a per-file digest table alongside the commit.** Rejected as redundant
  with the commit hash (see §2).
- **Merge the new pack over the existing directory instead of replacing it.**
  Rejected: a file removed from a skill would survive forever, and an agent
  would keep reading instructions gplay no longer ships.
