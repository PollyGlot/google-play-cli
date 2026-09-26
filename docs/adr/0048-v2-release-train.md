# The 2026-09 audit ships as one major release, 2.0.0

## Status

accepted, amends [ADR-0010](./0010-versioning-public-contract-and-ga.md); decisions 1 and 2 superseded by the amendment below (2026-09-26)

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

## Amendment (2026-09-26): release continuously, 2.0.0 when the breaks land

Decisions 1 and 2 above are withdrawn the same day, before any release was cut,
after comparing with a peer CLI that ships near-daily releases and bumps its
major version whenever a breaking change lands (five majors in five months,
each a normal batch of fixes plus one break). Holding every fix for one major
would have kept live bugs (a credential echoed on error, `--retry` replaying a
refund) in the published 1.6.1 for weeks, and made 1.6.1 to 2.0.0 one diff too
large to bisect.

What holds now:

1. **Releases stay continuous.** Audit pull requests merge as they are ready
   and ship through the normal release-please flow as 1.x minors and patches.
   `release-as` is removed from `release-please-config.json`.
2. **2.0.0 is triggered by the breaks, not by a date.** The first pull request
   that lands a contract break decided in the Decisions milestone (#597 flag
   and verb names, #599 the error envelope) carries the `!` marker, which makes
   release-please propose 2.0.0. All the break pull requests merge back to
   back and the 2.0.0 release pull request is merged only once every one of
   them is in: a break that misses 2.0.0 would force 3.0.0.
3. Decisions 3 and 4 stand: breaks decided in the Decisions milestone land
   without deprecation aliases and cite this ADR, and 2.0.0 ships with its
   1.x to 2.0 migration guide. The 2.0.0 announcement summarises everything
   shipped since 1.6.1.
