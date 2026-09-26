# The 2026-09 audit ships as one major release, 2.0.0

## Status

accepted, amends [ADR-0010](./0010-versioning-public-contract-and-ga.md) for one release

## Context

The 2026-09 audit ([PRD #573](https://github.com/PollyGlot/google-play-cli/issues/573),
milestones *Audit 2026-09 · Wave 0/1/2* and *Decisions*) produces a long series
of pull requests: a development framework (request executor, test kit,
ratchets, a golden of the command surface), bug fixes, hardening, and a
handful of contract decisions (#597 flag and verb names, #598 Edit commit
semantics, #599 the error envelope's `package` field) that break the frozen
Public surface [ADR-0010](./0010-versioning-public-contract-and-ga.md) and
[ADR-0042](./0042-one-zero-ga-and-stability-label-mechanism.md) protect.

Shipping them as they land would publish a dozen 1.x releases, then a major
whose breaks each need a deprecation alias for one more 1.x cycle.

## Decision

1. **One release.** Everything merged for the audit ships together as
   **2.0.0**. Pull requests merge to `main` as they are ready (trunk-based,
   squash); no release pull request is merged until the audit's milestones are
   closed.
2. **The version is pinned, not inferred.** `release-please-config.json`
   carries `"release-as": "2.0.0"` for the duration of the train, so the open
   release pull request reads `chore: release 2.0.0` from the first merge and
   cannot be mistaken for a patch. The key is removed in the first pull request
   after 2.0.0 is tagged; left in place, every later release would try to be
   2.0.0 again.
3. **Breaks are clean.** A contract change decided in the Decisions milestone
   lands without a deprecation alias: 2.0.0 is the major bump ADR-0010 reserves
   for it. Each such pull request references this ADR, which is what the
   frozen-surface gate (`scripts/contract-gate.sh`) asks of a PR that removes
   or changes a `frozen` line of `cmd/gplay/testdata/surface.golden`.
4. **The release carries its migration guide.** Before the release pull request
   merges, the website gains a 1.x to 2.0 migration page listing every removed
   or renamed flag, verb, key and exit-code change, generated from the golden
   diff between `v1.6.1` and `main` where possible.

## Consequences

- Fixes merged during the train, security fixes included, reach users only at
  2.0.0. Anyone needing one earlier builds from `main`
  (`go install github.com/PollyGlot/google-play-cli/cmd/gplay@main`).
- Consumers of the JSON contract (storedeck, agent skills) upgrade once, against
  one migration guide, instead of tracking a series of deprecations.
- After 2.0.0, ADR-0010 applies unchanged: frozen means frozen until 3.0.0.
