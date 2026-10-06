# Play Asset Delivery (asset packs)

gplay has no dedicated Play Asset Delivery (PAD) command.

## Why this is out of scope

Asset packs (install-time, fast-follow, on-demand) are configured on the
project side, as Gradle asset-pack modules, and they reach Play inside the
Android App Bundle. `gplay releases upload` already ships that bundle, asset
packs included. Once the bundle is built there is no admin API call left to
wrap, so this is a build-system concern, not an API blind spot under
[ADR-0026](../docs/adr/0026-maximal-admin-api-coverage.md).

If Google adds an admin method that operates on asset packs on their own, that
method shows up in the Discovery snapshot and goes through normal triage.

## Prior requests

- #523: "Parking: Play Asset Delivery (PAD) — dynamic asset packs"
