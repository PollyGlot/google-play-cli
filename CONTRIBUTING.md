# Contributing to gplay

Thanks for your interest. This project is pre-1.0 — the design is moving
fast and contributions are very welcome.

## Before you start

1. **Read [AGENTS.md](AGENTS.md) first.** It lists the docs to read in order
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

make format    # (will exist once the Go scaffold lands)
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

Conventional Commits style is preferred but not strictly enforced (yet):

```
feat: add --staged flag to releases promote
fix: handle malformed AAB upload with exit code 20
docs: clarify safe-defaults rule on production track
```

## What goes where

- **CLI convention or cross-command behavior change** → also update
  `docs/DESIGN.md` in the same PR.
- **New canonical term** (a new domain noun) → add to `CONTEXT.md`.
- **Irreversible / surprising decision** → add an ADR under `docs/adr/`.
- **Feature you decided to defer** → add to `docs/BACKLOG.md` with rationale.

## Code review

Every PR needs at least one approval (admin can bypass in early bootstrap).
The reviewer checks:

1. The change matches the docs (or updates them).
2. Tests cover the new behavior (RoundTripper-mocked, see AGENTS.md).
3. `--help` text and output for new flags follow `docs/DESIGN.md`.
4. No accidental scope creep from the backlog.

## Code of Conduct

This project follows the [Contributor Covenant](CODE_OF_CONDUCT.md). Be
kind, be specific, focus on the work.

## Reporting security issues

**Do not open a public issue.** Email `paul.trinko95@gmail.com` or use
GitHub's private vulnerability reporting. See [SECURITY.md](.github/SECURITY.md).
