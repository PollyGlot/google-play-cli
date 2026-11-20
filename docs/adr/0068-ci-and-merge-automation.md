# ADR-0068: What the CI and maintenance scripts do, and the platform rules they work around

## Status

accepted (2026-11-20)

## Context

The workflows under `.github/` and the scripts under `.github/scripts/` and
`scripts/` merge pull requests without a human, fire agent tasks, edit release
notes and keep the local gate identical to CI. Each one is shaped by a rule of
the GitHub platform, of `gh`, or of the shell it runs in, that the script
itself cannot show. These decisions lived in code comments; the comment purge
(the `scripts/lint/comments.py` ratchet) gathers them here.

[docs/CI_CD.md](../CI_CD.md) describes the workflows and their triggers; this
ADR records why the scripts behind them are built the way they are. It is a
catalogue: a new script, or a new constraint on an existing one, gets an entry
under the section it concerns.

## Decision

### Merging as the solo maintainer (`scripts/merge-pr.sh`)

The ruleset requires an approving review and up-to-date checks, no approval can
exist with one maintainer, and `gh pr merge --admin` skips the up-to-date rule
too (the story is in docs/CI_CD.md). The script puts that rule back.

- **Both sides are fetched fresh before comparing.** A stale local
  `origin/<base>` would let a behind branch through, which is the exact
  failure the script exists to stop.
- **The required checks are read from the branch rules** (ruleset or classic
  protection), never from a list kept in the script, which would drift from
  GitHub.
- **The base is read once more just before merging.** A merge that landed
  while the checks were being read would leave the PR behind again; the
  remaining window is seconds wide.
- **The merge passes `--match-head-commit <sha>`.** A push to the PR between
  the checks and the merge then makes GitHub reject the merge, instead of
  squashing a head nobody verified.
- **The draft gate is checked last.** It is the cheapest to clear, so the
  "behind" and "checks red" refusals speak first.

### Dependabot auto-merge (`.github/scripts/dependabot-merge.sh`)

- **One merge per run.** A merge puts every other PR behind `main`, and
  `merge-pr.sh` refuses a behind head. Each run merges at most one eligible PR
  and brings every other eligible PR up to date, so its CI runs now and the
  next run can merge it. Once a merge happened, every remaining PR is treated
  as behind, whatever state was read before it.
- **Eligible means opened by Dependabot, not a draft, and every announced
  version move keeps its major.** Below 1.0 a minor may break too, but
  `golang.org/x/*` and most actions here live at `0.x` and every such bump has
  been merged as is: CI is the gate for those. A major bump, a PR announcing
  no version, or a red check stays open for the maintainer, and the job
  summary says which and why.
- **The version moves are read from the start of a line.** Dependabot
  announces each move on a line of its own (`Bumps [x](...) from A to B.` for
  one update, ``Updates `x` from A to B`` per package of a group); the release
  notes it quotes below also say "from ... to ...".
- **The token is the gplay App token, not `GITHUB_TOKEN`.** A branch update
  pushed with `GITHUB_TOKEN` starts no check run, so the PR would never turn
  green.

### Discovery bot merges

- **Both bot merge paths re-derive the blast radius from the PR's own file
  list** (`.github/scripts/discovery-blast-radius.sh`), shared by the
  revision-only auto-merge and the verdict merge. Whatever produced the label
  ran at a different time against a force-pushed branch, so the label is not
  trusted.
- **The guard approves the file list of one exact commit.** `gh pr diff` only
  reads the live head, so the guard reads the head before and after the diff
  and refuses when either differs from the sha the caller is about to pass to
  `--match-head-commit`.
- **The allow-list is the generated files only**: the snapshot directory and
  the schema index derived from it. A caller that legitimately expects one more
  file passes it as an extra alternative (the triage routine commits
  `docs/COVERAGE.md` on the branch).
- **A failed `gh pr diff` fails the guard.** Its output is captured on its own
  line, never piped into `grep`, where a failure would read as an empty, hence
  clean, file list.
- **The only code change the triage routine may push**, new runtime
  exclusions in `internal/apiregistry/exclusions.go`, is checked by
  `.github/scripts/discovery-exclusions-guard.sh` after the blast-radius
  guard (rules in [docs/agents/discovery-triage.md](../agents/discovery-triage.md)).
  It must run from a `main` working tree: the "a sibling is already excluded"
  rule reads the local file as the state of `main`.
- **The `discovery:*` labels are machine state.** `discovery:schema` and
  `discovery:surface` are set by the watch workflow, `discovery:verdict-merge`
  and `discovery:needs-decision` by the triage routine, and CI reads them back
  to merge or to page the maintainer. Renaming one in `scripts/sync-labels.sh`
  breaks that automation.

### Secrets in workflow scripts

- **An agent task is fired through a signed webhook**
  (`.github/scripts/fire-t3-task.sh`). The task checks an HMAC-SHA256 of the
  raw body in GitHub's webhook format (`X-Hub-Signature-256: sha256=<hex>`), so
  a leaked URL alone cannot start a run.
- **The webhook URL is a secret.** The script prints only the HTTP status and
  leaves the response body in a file.
- **The signing key is read from the environment, never passed as an
  argument**, so it does not show in the runner's process list.
- **The URL and the signature go to `curl` through a config on stdin, not
  argv.** With both, anyone reading the process list could replay a valid
  request.
- **One attempt, no retry.** A second delivery means a second run.

### Release contributors (`.github/scripts/release-contributors.sh`)

- **The pull requests come from GitHub's release-notes generator; its "New
  Contributors" block is not used.** It missed the first merged PR of an
  outside contributor, so a newcomer is instead someone whose earliest merged
  PR is in this release.
- **Bots are dropped** (Dependabot, the gplay App, release-please), and so is
  Claude: it only appears as a `Co-authored-by` trailer, which the generator
  does not read.
- **The previous release is passed explicitly**: the previous non-draft,
  non-prerelease release in semver order. Left to itself, the generator picks
  the latest release older than the tag by date, which a backfill of old
  releases gets wrong.
- **Numbers cited on changelog lines that the generator did not list are looked
  up one by one**, because the generator skips some merged PRs. An issue number
  answers 404 and is skipped; a bot author is skipped too.
- **The section sits between two HTML comment markers** in the release body,
  replacing any earlier copy, so a rerun or a manual replay never duplicates
  it.
- **Each changelog line is credited to its PR author**, as GitHub-generated
  notes do: ` by @login` goes at the end of the release-please line
  (`* **scope:** title ([#N](url)) ([sha](url))`), and a line already
  credited is left alone.

### `gh` and GitHub API behaviours

- **`gh` resolves `{owner}/{repo}` from the git remote**, so a script that
  calls it changes to its own repository first, whatever the caller's working
  directory.
- **`gh pr checks` exits non-zero while checks fail (1) or are pending (8),
  even with `--json`.** The exit code is ignored and the JSON read instead. An
  empty answer (gh failed outright) leaves every required check missing, which
  refuses.
- **`gh pr diff --name-only` prints only the new name of a renamed or copied
  file**, so moving a file into `docs/discovery/` would read as a snapshot
  change. The names come from git's extended headers instead: `rename|copy
  from|to <path>` carries each name whole, otherwise `diff --git a/<p> b/<p>`
  names one path twice and splits by length even when the path contains
  ` b/`. Content lines always start with a space, `+` or `-`, so the patterns
  only match headers. Any other header (a quoted path) is printed raw, matches
  no allowed prefix, and fails closed; a rename whose `to` line is missing
  fails closed too.
- **A `::error::` prefix** annotates the file in the GitHub UI and still reads
  as plain text locally.

### Developer tooling

- **The em dash is kept out of Go source** (`scripts/dash-gate.sh`). It is
  the most recognisable marker of machine-written prose, and Go strings leak
  to users: `Short` and `Long` become `--help` and the generated reference
  pages, error strings become stderr. Wording is outside the Public contract
  (DESIGN §7), so nothing is preserved by allowing it. `_test.go` files are
  exempt: their fixtures simulate user content (a review reply, a store
  listing) where an em dash is legitimate data.
- **Hooks install as a shim, not through `core.hooksPath`**
  (`scripts/install-hooks.sh`). The shim goes in the directory
  `git rev-parse --git-path hooks` names, because in a worktree `.git` is a
  file and the hooks directory is shared through the common dir: one install
  covers every worktree. Repointing `core.hooksPath` would silently disable
  hooks already installed there, and a delegating shim never goes stale when
  `.githooks/` changes; a branch that predates `.githooks/` simply skips.
- **One list of required files for two callers** (`scripts/required-files.sh`):
  the "Docs sanity" required check and `make check`. Keeping it out of the
  workflow is what keeps the local gate identical to CI.
- **The label set is code** (`scripts/sync-labels.sh`). A label created by hand
  on GitHub joins the list in the same change, or the next `--check` reports
  it. The `autorelease:` labels are listed although nothing in the repository
  uses them: release-please sets them on its release PR, and `--check` would
  otherwise fail on every release.

### Shell portability

- **A tool whose absence must be reported is resolved before the pipeline.** In
  `shasum | awk`, the pipeline exits with awk's status, so a missing `shasum`
  would leave an empty digest reported as a bogus mismatch instead of the real
  cause.
- **`comm` needs both sides sorted under the same collation**, so both sorts
  run under `LC_ALL=C`.
- **A list whose items may contain spaces is read line by line, never with a
  `for` over word splitting** (label names such as "good first issue").

## Consequences

- Each entry is a contract with GitHub, `gh` or the shell, not with users: a
  platform change can silently break it, and the guards are written to fail
  closed when it does.
- A new automated merge path reuses `merge-pr.sh` or the blast-radius guard
  rather than re-implementing their checks.
