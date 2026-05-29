# Release pipeline: release-please owns versioning, GoReleaser owns artifacts

gplay cuts releases by composing two tools that own different stages of the
pipeline:

- **release-please** decides the next version from Conventional Commits,
  regenerates `CHANGELOG.md`, and maintains a rolling "release" PR. Merging
  that PR creates the `vX.Y.Z` tag and the GitHub Release.
- **GoReleaser** builds the multi-platform binaries, archives, checksums,
  SBOMs and the cosign signature, and publishes them to that release plus the
  Homebrew tap.

The two never overlap except on one point — both can create a GitHub Release —
which is resolved by `release.mode: keep-existing` in `.goreleaser.yaml`:
release-please creates the release with its changelog body, GoReleaser only
attaches artifacts without touching that body. Ordering is guaranteed by the
`needs:` dependency in `.github/workflows/release-please.yml`, which calls
`release.yml` as a reusable workflow once a release is cut.

## Why

1. **Separation of concerns, not redundancy.** release-please answers "what's
   the next version and what changed"; GoReleaser answers "produce and ship the
   artifacts." Neither can do the other's job — release-please cannot compile a
   Go binary, GoReleaser does not decide the semver bump or curate an
   accumulating `CHANGELOG.md`. Composing them is the documented pattern, not a
   workaround.

2. **No more hand-cut tags.** Before this, releasing meant manually editing
   `CHANGELOG.md`, running `git tag`, and pushing — with the standing risk of a
   tag/changelog desync or a forgotten bump. Now the human action is "merge the
   release PR." This was the single manual step worth automating; the rest of
   the pipeline was already solid.

3. **No second PAT.** release-please runs on `push: main` with the default
   `GITHUB_TOKEN` and calls `release.yml` in the same workflow run via
   `workflow_call`. Because GoReleaser runs as a dependent job rather than via a
   cascaded tag-push event, we avoid the "GITHUB_TOKEN doesn't trigger
   workflows" cascade problem and the extra personal access token it would
   otherwise require. (The Homebrew tap still needs its existing
   `HOMEBREW_TAP_GITHUB_TOKEN`.)

4. **Supply-chain provenance at no marginal cost.** Once GoReleaser owns the
   build, signing checksums with keyless cosign (OIDC, `id-token: write`) and
   emitting an SPDX SBOM per archive is a few lines of config. gplay's pitch is
   that third-party CI pipelines pull the binary; signed checksums and an SBOM
   are what let those consumers verify what they run.

## What we lose

- **Two configs instead of one.** `release-please-config.json` +
  `.release-please-manifest.json` for versioning, `.goreleaser.yaml` for
  artifacts. The cognitive cost is real but bounded — each file owns one stage.
- **An implicit ordering dependency.** GoReleaser must run after release-please
  created the release, or it would create one with GitHub-native notes instead
  of the curated changelog. The `needs:` edge enforces this; breaking it would
  silently degrade the release body, not fail loudly.
- **The manual-tag path no longer gets the curated changelog.** Pushing a
  `vX.Y.Z` tag by hand still works (escape hatch) but produces GoReleaser's own
  release header, since release-please isn't in that loop.

## Versioning strategy

gplay follows **0.x semver** while pre-1.0: `fix:` bumps patch, `feat:` bumps
the **minor** (`0.1.0` → `0.2.0`), and breaking changes also bump the minor
rather than the major until `v1.0`. This is release-please's default behavior
for `0.y.z`, so it needs no extra config. `1.0.0` is cut when the command
surface — flags, JSON output shapes ([ADR-0003](0003-json-passthrough.md)), and
the exit-code table — is stable enough that breaking it warrants a major bump.

Versions never reset or move backward: every tool that resolves "latest"
(`go install @latest`, Homebrew, release-please) orders by semver, so a lower
version would rank below existing releases and be invisible.

## Considered Options

- **GoReleaser alone, manual tags** — rejected. Keeps the manual `git tag` +
  changelog step this ADR exists to remove. Fine for a project that releases
  rarely; gplay ships per-PR often enough that the automation pays off.
- **GoReleaser alone with its commit-grouped changelog (or git-cliff)** —
  rejected. Improves release notes without a second orchestrator, but still
  leaves the version-bump decision and tagging manual.
- **release-please alone** — impossible. It cannot build Go binaries, archives,
  the Homebrew formula, SBOMs, or signatures. GoReleaser is not optional.
- **Resetting to `0.0.x`** — rejected. Violates monotonic semver; `@latest`
  resolution would keep pointing at the higher existing `0.1.0-alpha.2`.

## How this shows up to users

The release badge (`shields.io/github/v/release`) and the `gplay version`
output (ldflags-stamped) update automatically; nothing in the README hardcodes
a version. End users see signed checksums and an SBOM attached to each release,
and a `CHANGELOG.md` that reads cleanly because it is generated from
Conventional Commit subjects — which is why [CONTRIBUTING.md](../../CONTRIBUTING.md)
asks for them.
