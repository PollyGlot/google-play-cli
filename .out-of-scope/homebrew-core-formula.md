# homebrew-core formula under the bare `gplay` name

gplay is not submitted to homebrew-core: it ships from its own tap,
`brew install --cask PollyGlot/tap/gplay`, and stays there.

## Why this is out of scope

The gain is cosmetic: `brew install gplay` instead of
`brew install PollyGlot/tap/gplay`. Against it:

- **Build from source.** homebrew-core builds formulae from source on its own
  bottlers. The tap ships the exact archives the release pipeline built, signed
  and checksummed ([ADR-0008](../docs/adr/0008-release-pipeline.md)); a core
  bottle is a second build of the same tag that gplay does not control.
- **Release cadence.** goreleaser pushes the tap directly on every release, so
  a new version is installable minutes after the tag. A core formula moves at
  the pace of homebrew-core review and its autobump, out of the maintainer's
  hands.
- **Name collision.** Another project,
  [tamtom/play-console-cli](https://github.com/tamtom/play-console-cli), also
  ships a binary named `gplay`. The bare name in core would claim a name two
  tools already use, and whichever lands second breaks the other's users.

## Prior requests

- #525: "Parking: graduate the Homebrew formula to homebrew-core (bare `gplay`
  name)"
