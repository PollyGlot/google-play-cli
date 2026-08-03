# gplay — Project Context

A Go CLI for the Google Play Developer API — one static binary, built for CI
and AI agents, replacing Fastlane/Ruby. Pre-1.0.

## Read these first

1. **`CONTEXT.md`** — glossary of canonical terms (`Edit`, `Account`,
   `Project`, …). Use them **verbatim**; no synonyms.
2. **`docs/DESIGN.md`** — single source of truth for cross-command behavior
   (auth precedence, exit codes, output, verbosity, edit lifecycle).
3. **`docs/BACKLOG.md`** — not-yet-shipped surfaces and their ordering. Every
   Play **admin** API is in scope (ADR-0026); only runtime surfaces (Play
   Integrity, real-time purchase verification) are excluded by nature.
   **`docs/COVERAGE.md`** is the orthogonal view — the method-level matrix of all
   ~155 admin methods mapped to shipped/planned/excluded (the blind-spot guard).
4. **`docs/adr/`** — the surprising / irreversible decisions and their why.

## Non-obvious rules (what breaks if you guess)

- **Raw HTTP, not the Go SDK.** gplay hand-rolls every Developer API call in
  `internal/play/api/`. Do **not** add `google.golang.org/api/androidpublisher`
  ([ADR-0007](docs/adr/0007-raw-http-not-google-go-sdk.md)); auth uses
  `golang.org/x/oauth2/google`.
- **`production` releases default to `draft`** unless `--complete`/`--staged`
  ([ADR-0002](docs/adr/0002-safe-production-defaults.md)).
- **`--output json` is API pass-through** — mirrors the response verbatim
  ([ADR-0003](docs/adr/0003-json-passthrough.md)). **stdout = data, stderr = logs.**
- **Discovery snapshot is query-only** — `grep docs/discovery/paths.txt` or `jq`
  the snapshot to check a method's shape. Never read it whole.

## Skills (companion repo)

Agent skills that drive `gplay` from natural-language prompts live in the public
sibling repo `PollyGlot/google-play-cli-skills` — one per shipped namespace plus
a `gplay-cli-usage` foundation. Install:
`npx skills add PollyGlot/google-play-cli-skills`.

## Build & test

- **The CLI is self-documenting** — run `--help` to confirm the interface
  before coding or testing. Don't memorize commands.
- **Tests never touch the network** — mock with a `testRoundTripper`
  (`http.RoundTripper`) via `option.WithHTTPClient(...)`. No mock generation.
- **Gate before every PR** (CI + pre-commit enforce): `make format lint test
  verb-gate` (verb-gate blocks pre-rename verbs, ADR-0019).

## Pull requests

- **Docs-only PRs: merge without asking.** If a PR changes *only* documentation
  — Markdown and doc assets (`*.md`: `README`, `CLAUDE.md`, `CONTEXT.md`,
  everything under `docs/` incl. ROADMAP/BACKLOG/DESIGN/ADRs) — squash-merge it
  to `main` yourself, no confirmation needed. `main` is review-protected, so the
  sanctioned mechanism is the admin override `gh pr merge <n> --admin --squash`
  — but **only after you have confirmed every CI check is green** (`--admin`
  bypasses required checks too, so you are the gate; never override a failing or
  pending run). **Docs-only means zero non-doc files**: any change under
  `cmd/**`, `commands/**`, `internal/**` (*whatever the extension* — the binary
  embeds JSON and CSV too), or to `Makefile`, `.github/**`, `go.mod`/`go.sum`,
  install/release scripts, or `docs/discovery/**` (Discovery snapshots are build
  inputs, not docs) disqualifies the PR — those still need explicit approval and
  a normal reviewed merge. This list is mirrored by the `code` path filter in
  [`ci.yml`](.github/workflows/ci.yml); keep the two in sync.

## Commit types & releases

- **Site/docs/CI-only changes use `docs(...)` or `chore(...)` — never `feat`/
  `fix`.** release-please is one root package (`release-type: simple`,
  [`release-please-config.json`](release-please-config.json)) and bumps the CLI
  version off the conventional-commit **type**, blind to scope and changed
  paths: a `feat(...)` → minor, a `fix(...)` → patch, *whatever it touched*. So
  `feat(site): …` cuts a CLI release whose binary is byte-identical to the
  previous one and whose changelog credits a non-CLI "feature". Reserve `feat`/
  `fix` for changes to the shipped binary; type everything under `website/`,
  `docs/`, `.github/`, and other non-binary surfaces as `docs`/`chore`/`ci`.
- **Nothing is lost by doing this.** The site deploys on its own
  (`deploy-site.yml` triggers on the `website/**`/`deploy/gplay.sh/**` *path*,
  not the commit type), so a `docs`/`chore` site commit still ships — it just
  doesn't bump the CLI version. A version bump should mean the binary changed.

## Adding a command

1. **In scope?** Check `docs/BACKLOG.md` — surface the decision, don't silently
   promote a backlog item.
2. **Term check.** New domain noun → confirm/add in `CONTEXT.md`, no synonyms.
3. **Conventions.** Apply the relevant `docs/DESIGN.md` section.
4. **Test first**, RoundTripper-mocked.
5. **Update `--help`** and command docs (use `CONTEXT.md` terms).
6. **Decide its stability.** Since 1.0 an unlabelled command joins the frozen
   Public contract (ADR-0010/ADR-0042). Not ready to promise its flags forever?
   `kernel.Experimental(...)` at the registration site. The registry test in
   `cmd/gplay` fails on any unclassified leaf.
