# Contributing to gplay

Thanks for your interest. This project is pre-1.0 — the design is moving
fast and contributions are very welcome.

## Before you start

1. **Read [CLAUDE.md](CLAUDE.md) first.** It lists the docs to read in order
   and the conventions used across every command. Most "should I do X or Y?"
   questions have an answer in `docs/DESIGN.md` or the ADRs.
2. **Cross-check [docs/BACKLOG.md](docs/BACKLOG.md).** If the feature you
   want to add is in there, surface that in an issue *first* rather than
   silently expanding scope. Backlog items are deferred on purpose.
3. **Open an issue for anything non-trivial.** A short discussion saves
   time when the design touches a CLI convention. Typos and pure refactors
   can go straight to PR.

## Workflow

```bash
# Fork on GitHub, then:
git clone git@github.com:<you>/google-play-cli.git
cd google-play-cli
git checkout -b feat/short-description

# ... change code ...

make format
make lint
make test

git commit -m "feat: short description"
git push -u origin feat/short-description
# Open a PR against main.
```

Branch naming, loose convention:

- `feat/<slug>` — new functionality
- `fix/<slug>` — bug fix
- `docs/<slug>` — documentation only
- `chore/<slug>` — tooling, CI, deps

## Commit messages

[Conventional Commits](https://www.conventionalcommits.org/) style is
**recommended but not enforced**. PR titles follow the same convention
— GitHub squash-merges them onto `main`, so a clean title becomes a clean
log entry and feeds the auto-generated release notes (`gh release ...
--generate-notes`).

Prefixes we use:

| Prefix | When |
|---|---|
| `feat:` | New user-facing functionality, command, or flag |
| `fix:` | Bug fix that changes user-facing behavior |
| `docs:` | README, ADRs, CLAUDE.md, glossary, comments-only changes |
| `refactor:` | Internal restructuring with no behavior change |
| `test:` | Tests added or improved |
| `chore:` | Tooling, CI, dependencies, release plumbing |
| `perf:` | Performance change |
| `build:` | Build system or distribution (`.goreleaser.yaml`, `Makefile`) |

Optional scope in parentheses points at the affected area, matching the
`area:*` labels: `feat(releases): ...`, `fix(auth): ...`.

A `!` after the type (or a `BREAKING CHANGE:` footer) marks a
backwards-incompatible change — those PRs also get the `breaking-change`
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
minor, `fix` → patch), **not** from the files touched — the scope is invisible
to it. So `feat(website): ...` is treated as a feature of the `gplay` binary:
it bumps the version, writes a line into `CHANGELOG.md`, and the merged release
PR tags a build that ships no CLI change.

The site is decoupled by design — it deploys on its own pipeline
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
- **Feature you decided to defer** → add to `docs/BACKLOG.md` with rationale.
- **New command that mutates Google Play state** → wrap it with
  `kernel.MarkMutating(...)` at its registration site, so the `GPLAY_READONLY`
  policy refuses it (exit 4, ADR-0024). The mutating-registry guard test in
  `cmd/gplay` fails if a write command is left unmarked (or a read command is
  wrongly marked). Read commands and `--dry-run` paths stay unmarked.
- **New command taking positional arguments** → just declare the cobra
  validator (`Args: cobra.ExactArgs(1)`, …) and stop there. `kernel.WrapArgErrors`
  re-types every registered validator's rejection as CLI misuse (exit 2,
  `docs/DESIGN.md` §9) from one call at the end of `newRootCmd` — the hidden
  `__complete` shell plumbing is the one exception — so a hand-rolled
  per-command arg check is duplicated work that will drift (#426). The
  tree-walking guard test in `cmd/gplay` fails if any validator ever reports
  something else. This applies only through the assembled root: a per-package
  test that executes a bare `NewCommand(boot)` exercises the unwrapped
  validator (exit 1) — assert exit codes through `newRootCmd`, not in leaf
  harnesses.
- **Any new command, full stop** → decide its stability. Since 1.0, an
  unlabelled command joins the frozen Public contract: its names, flags,
  semantics and exit codes cannot change without a major bump (ADR-0010 /
  ADR-0042). If you are not ready to promise that, wrap it with
  `kernel.Experimental(...)` at its registration site. The stability-registry
  guard test in `cmd/gplay` fails on any leaf that is not explicitly
  classified, so this cannot be forgotten silently.

## Code review

Every PR needs at least one approval (admin can bypass in early bootstrap).
The reviewer checks:

1. The change matches the docs (or updates them).
2. Tests cover the new behavior (RoundTripper-mocked, see CLAUDE.md).
3. `--help` text and output for new flags follow `docs/DESIGN.md`.
4. No accidental scope creep from the backlog.

## GitHub Actions are SHA-pinned

Every third-party action in `.github/workflows/` is pinned to a **full commit
SHA** with a trailing version comment, e.g.:

```yaml
- uses: actions/checkout@df4cb1c069e1874edd31b4311f1884172cec0e10 # v6.0.3
```

A moving tag (`@v6`) can be force-pushed or hijacked; a SHA cannot. When adding
or editing a workflow, pin new actions the same way (resolve the tag with
`gh api repos/<owner>/<repo>/commits/<tag> --jq .sha`). Dependabot
(`.github/dependabot.yml`, weekly) proposes SHA bumps with a refreshed comment —
review and merge those rather than hand-editing pins.

## Code of Conduct

This project follows the [Contributor Covenant](CODE_OF_CONDUCT.md). Be
kind, be specific, focus on the work.

## Reporting security issues

**Do not open a public issue.** Use GitHub's private vulnerability reporting.
See [SECURITY.md](.github/SECURITY.md).
