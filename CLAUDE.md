# gplay — Project Context

A Go CLI for the Google Play Developer API — one static binary, built for CI
and AI agents, replacing Fastlane/Ruby. Companion agent skills live in the
public sibling repo `PollyGlot/google-play-cli-skills`. Pre-1.0.

## Read these first

1. **`CONTEXT.md`** — glossary of canonical terms (`Edit`, `Account`,
   `Project`, …). Use them **verbatim**; no synonyms.
2. **`docs/DESIGN.md`** — single source of truth for cross-command behavior
   (auth precedence, exit codes, output, verbosity, edit lifecycle).
3. **`docs/BACKLOG.md`** — out-of-scope surfaces; check before adding "X".
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
- **Tests never touch the network** — mock with a `testRoundTripper`
  (`http.RoundTripper`) via `option.WithHTTPClient(...)`. No mock generation.
- **Discovery snapshot is query-only** — `grep docs/discovery/paths.txt` or `jq`
  the snapshot to check a method's shape. Never read it whole.

## Working in the repo

- **The CLI is self-documenting** — run `--help` to confirm the interface
  before coding or testing. Don't memorize commands.
- **Gate before every PR** (CI + pre-commit enforce): `make format lint test
  verb-gate` (verb-gate blocks pre-rename verbs, ADR-0019).
- **Adding a command:** in scope? (check `BACKLOG.md`) → new noun? (add to
  `CONTEXT.md`) → apply the relevant `DESIGN.md` section → test first
  (RoundTripper-mocked) → update `--help`.
