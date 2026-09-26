# Contributing to gplay

Thanks for your interest. gplay is past 1.0: every command not marked
experimental is a frozen Public contract (ADR-0042), and contributions are
very welcome.

## Before you start

1. **Read [AGENTS.md](AGENTS.md) first.** It lists the conventions used
   across every command and the traps of this repo. Most "should I do X or Y?"
   questions have an answer in `docs/DESIGN.md` or the ADRs.
2. **Search the [parking issues](https://github.com/PollyGlot/google-play-cli/issues?q=is%3Aissue+label%3Atype%3Aparking).** If the feature
   you want to add is already tracked there, comment on it *first* rather
   than silently expanding scope. Parked items are deferred on purpose, and
   the issue carries the reason.
3. **Open an issue for anything non-trivial.** A short discussion saves
   time when the design touches a CLI convention. Typos and pure refactors
   can go straight to PR.

## Prerequisites

- Go, at the version in `go.mod` or newer.
- [golangci-lint](https://golangci-lint.run/) v2. CI pins the exact version in
  `.github/workflows/ci.yml`; `make check` warns when yours differs.
- [shellcheck](https://www.shellcheck.net/), `make` and `bash`.

## Workflow

```bash
# Fork on GitHub, then:
git clone git@github.com:<you>/google-play-cli.git
cd google-play-cli
git checkout -b feat/short-description

# ... change code ...

make format   # gofmt + goimports, the formatters CI enforces
make check    # the required CI checks, run locally: the one gate before a PR

git commit -m "feat: short description"
git push -u origin feat/short-description
# Open a PR against main.
```

`make check` runs everything the required CI checks run (formatting, `go vet`,
golangci-lint, the prose gates, shellcheck, the install script test, the
required files, the build and `go test -race`). `make test` stays the fast
loop without the race detector. To run the gate automatically before every
push, opt in once with `make install-hooks` (works from a git worktree too;
skip a single push with `git push --no-verify`).

Branch naming, loose convention:

- `feat/<slug>`: new functionality
- `fix/<slug>`: bug fix
- `docs/<slug>`: documentation only
- `chore/<slug>`: tooling, CI, deps

## Commit messages

PR titles follow [Conventional Commits](https://www.conventionalcommits.org/).
GitHub squash-merges them onto `main`, and
[release-please](https://github.com/googleapis/release-please) reads the type
of each commit there to cut the next release: `feat` bumps the minor version,
`fix` the patch, a `!` the major, and the same titles become the
`CHANGELOG.md` entries. Other types (`docs`, `chore`, `test`, ...) release
nothing, so the type you pick decides whether users get a new version.

Prefixes we use:

| Prefix | When |
|---|---|
| `feat:` | New user-facing functionality, command, or flag |
| `fix:` | Bug fix that changes user-facing behavior |
| `docs:` | README, ADRs, AGENTS.md, glossary, comments-only changes |
| `refactor:` | Internal restructuring with no behavior change |
| `test:` | Tests added or improved |
| `chore:` | Tooling, CI, dependencies, release plumbing |
| `perf:` | Performance change |
| `build:` | Build system or distribution (`.goreleaser.yaml`, `Makefile`) |

Optional scope in parentheses points at the affected area, matching the
`area:*` labels: `feat(releases): ...`, `fix(auth): ...`.

A `!` after the type (or a `BREAKING CHANGE:` footer) marks a
backwards-incompatible change; those PRs also get the `breaking-change`
label.

Examples:

```
feat(releases): add --staged flag to releases promote
fix(auth): surface missing androidpublisher scope with exit code 10
docs: clarify safe-defaults rule on production track
chore(ci): bump golangci-lint to v6
feat(tracks)!: rename --percentage to --rollout in releases promote
```

### Website / `gplay.sh` commits don't bump the CLI

release-please derives the version bump from the commit **type** (`feat` →
minor, `fix` → patch), **not** from the files touched: the scope is invisible
to it. So `feat(website): ...` is treated as a feature of the `gplay` binary:
it bumps the version, writes a line into `CHANGELOG.md`, and the merged release
PR tags a build that ships no CLI change.

The site is decoupled by design: it deploys on its own pipeline
([`deploy-site.yml`](.github/workflows/deploy-site.yml), triggered by
`website/**` + `deploy/gplay.sh/**` and on `release: published`), so a
site-only change is **not** a `feat`/`fix` of the binary. Type those commits:

| Use | For |
|---|---|
| `docs(website):` | Site content, copy, docs pages |
| `chore(website):` / `build(website):` | Worker, `wrangler.toml`, site CI/tooling |

None of those bump the version or land in the CLI `CHANGELOG.md`. Reserve
`feat`/`fix` for changes to the CLI itself.

## What goes where

- **CLI convention or cross-command behavior change** → also update
  `docs/DESIGN.md` in the same PR.
- **New canonical term** (a new domain noun) → add to `CONTEXT.md`.
- **Irreversible / surprising decision** → add an ADR under `docs/adr/`.
- **Feature you decided to defer** → file a `type:parking` issue with the rationale.
- **New command that mutates Google Play state** → wrap it with
  `kernel.MarkMutating(...)` at its registration site, so the `GPLAY_READONLY`
  policy refuses it (exit 4, ADR-0024). The mutating-registry guard test in
  `cmd/gplay` fails if a write command is left unmarked (or a read command is
  wrongly marked). Read commands and `--dry-run` paths stay unmarked.
- **New command taking positional arguments** → just declare the cobra
  validator (`Args: cobra.ExactArgs(1)`, …) and stop there. `kernel.WrapArgErrors`
  re-types every registered validator's rejection as CLI misuse (exit 2,
  `docs/DESIGN.md` §9) from one call at the end of `newRootCmd` (the hidden
  `__complete` shell plumbing is the one exception), so a hand-rolled
  per-command arg check is duplicated work that will drift (#426). The
  tree-walking guard test in `cmd/gplay` fails if any validator ever reports
  something else. This applies only through the assembled root: a per-package
  test that executes a bare `NewCommand(boot)` exercises the unwrapped
  validator (exit 1), so assert exit codes through `newRootCmd`, not in leaf
  harnesses.
- **Any new command, full stop** → decide its stability. Since 1.0, an
  unlabelled command joins the frozen Public contract: its names, flags,
  semantics and exit codes cannot change without a major bump (ADR-0010 /
  ADR-0042). If you are not ready to promise that, wrap it with
  `kernel.Experimental(...)` at its registration site. The stability-registry
  guard test in `cmd/gplay` fails on any leaf that is not explicitly
  classified, so this cannot be forgotten silently.

## Code review

The ruleset asks for one approval, which the solo maintainer cannot give to
their own PR, so the maintainer merges with `scripts/merge-pr.sh <n>`: it
refuses a PR that is behind `main` or has a required check that is not green,
then squash-merges with `--admin`. An external reviewer on a PR still gets the
last word. The reviewer checks:

1. The change matches the docs (or updates them).
2. Tests cover the new behavior (RoundTripper-mocked, see AGENTS.md).
3. `--help` text and output for new flags follow `docs/DESIGN.md`.
4. No accidental scope creep beyond the issue the PR closes.

## GitHub Actions are SHA-pinned

Every third-party action in `.github/workflows/` is pinned to a **full commit
SHA** with a trailing version comment, e.g.:

```yaml
- uses: actions/checkout@df4cb1c069e1874edd31b4311f1884172cec0e10 # v6.0.3
```

A moving tag (`@v6`) can be force-pushed or hijacked; a SHA cannot. When adding
or editing a workflow, pin new actions the same way (resolve the tag with
`gh api repos/<owner>/<repo>/commits/<tag> --jq .sha`). Dependabot
(`.github/dependabot.yml`, weekly) proposes SHA bumps with a refreshed comment:
review and merge those rather than hand-editing pins.

## Code of Conduct

This project follows the [Contributor Covenant](CODE_OF_CONDUCT.md). Be
kind, be specific, focus on the work.

## Reporting security issues

**Do not open a public issue.** Use GitHub's private vulnerability reporting.
See [SECURITY.md](.github/SECURITY.md).
