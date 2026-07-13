# API Coverage Matrix

**Single source of truth for how much of the Play admin API surface `gplay` covers.**
The [BACKLOG](BACKLOG.md) organises *not-yet-shipped* surfaces by theme and the
[ROADMAP](ROADMAP.md) orders the active work; this file is the orthogonal view —
**every method of every in-scope API, mapped to shipped / planned / excluded** —
so coverage can be verified at a glance and nothing stays a blind spot.

Policy: under [ADR-0026](adr/0026-maximal-admin-api-coverage.md) **every Play admin
API is in scope**; only *runtime* APIs (Play Integrity, real-time purchase
verification) are excluded by nature.

## How this is generated

Enumerated from the offline Discovery snapshots (query-only, never read whole —
see [discovery/README.md](discovery/README.md)):

```sh
# every method of an API: id + HTTP verb
jq -r '[.. | objects | select(has("id") and has("httpMethod"))
        | "\(.id)\t\(.httpMethod)"] | sort | .[]' \
  docs/discovery/androidpublisher_v3.json
```

Last refreshed against the snapshot at commit-time of this file (2026-06-22,
revision per [discovery/README.md](discovery/README.md)). **Re-run the query and
update this table whenever a slice ships or the snapshot is bumped.**

## Legend

| Mark | Meaning |
|---|---|
| ✅ | **Shipped** — a `gplay` command covers it |
| 🟡 | **Ready** — grilled + decomposed into `ready-for-agent` slices (P0) |
| 🔵 | **Planned** — on the backlog/roadmap, not yet decomposed |
| 🔴 | **Untracked** — in scope per ADR-0026 but no command and (until 2026-06-22) no issue |
| ⚫️ | **Excluded by nature** — runtime API, structurally unusable from a terminal |

## Headline

By method count, of the **~155 admin methods** across the four APIs:

- **~92 shipped (~59%)** — essentially the entire *publish / first-release /
  team / observability* half plus the admin commerce-reads and games config
  (incl. `edits.apks.upload` via `releases upload`, ADR-0036,
  `generatedapks`, PRD #299 / ADR-0034, `customapps`, PRD #242 / ADR-0032,
  **orders #245 in full** — `orders view` single + batch (`orders.get`/
  `orders.batchget`) and the gated `orders refund` (`orders.refund`), ADR-0031,
  and **games #241** — achievements + leaderboards CRUD, ADR-0033).
- **P0 queue now empty** — both ready-decomposed PRDs (orders #245, games #241)
  have shipped; the next work is the planned monetization block below.
- **~64 methods remain**, of which **~54 are one coherent continent:
  monetization** (subscriptions #51 + one-time products #293 + legacy IAP +
  external transactions #295); the other ~10 are long-tail (`edits.apks`,
  `systemapks`, voided purchases, the two reporting helpers). Monetization is the
  single biggest unbuilt block and the honest answer to "are we near the end?":
  **the commerce half is barely started.**
- **4 surfaces were genuinely untracked** until the 2026-06-22 coverage audit
  (issues #293–#296); `generatedapks` (#294) has since been grilled into PRD #299
  and **shipped** (slices #300–#301, PR #305).

---

## `androidpublisher` v3 — 137 methods

| Surface (resource) | Methods | State | gplay namespace / issue |
|---|---|---|---|
| `edits` lifecycle (`insert`/`get`/`commit`/`validate`/`delete`) | 5 | ✅ | internal edit machinery (explicit mode parked, [#48](https://github.com/PollyGlot/google-play-cli/issues/48)) |
| `edits.bundles` (AAB) | 2 | ✅ | `releases upload` |
| `edits.listings` | 6 | ✅ | `metadata` (`deleteall` parked, [ADR-0013](adr/0013-image-slot-reconciliation.md)) |
| `edits.images` | 4 | ✅ | `metadata images` (`deleteall` parked, ADR-0013 §3) |
| `edits.details` | 3 | ✅ | `apps details` |
| `edits.testers` | 3 | ✅ | `testers` |
| `edits.tracks` | 5 | ✅ | `tracks` |
| `edits.countryavailability` | 1 | ✅ | `tracks availability` |
| `edits.deobfuscationfiles` | 1 | ✅ | `releases mappings` |
| `edits.expansionfiles` | 4 | ✅ | `releases expansion-files` |
| `applications.dataSafety` | 1 | ✅ | `compliance` |
| `applications.deviceTierConfigs` | 3 | ✅ | `device-tiers` |
| `applications.tracks.releases.list` | 1 | ✅ | `releases list` |
| `apprecovery` | 5 | ✅ | `recovery` |
| `internalappsharingartifacts` | 2 | ✅ | `releases sharing` |
| `reviews` | 3 | ✅ | `reviews list`/`reply`/`view` ([#298](https://github.com/PollyGlot/google-play-cli/issues/298) shipped `reviews.get`) |
| `users` | 4 | ✅ | `team users` |
| `grants` | 3 | ✅ | `team grants` |
| `orders` | 3 | ✅ | `orders view <id>...` (`orders.get` single + `orders.batchget` batch) + `orders refund` (`orders.refund`, gated) — slices [#282](https://github.com/PollyGlot/google-play-cli/issues/282)/[#283](https://github.com/PollyGlot/google-play-cli/issues/283)/[#284](https://github.com/PollyGlot/google-play-cli/issues/284) ([#245](https://github.com/PollyGlot/google-play-cli/issues/245) / [ADR-0031](adr/0031-orders-commerce-reads-and-gated-refund.md)) |
| `edits.apks` | 3 | ✅ | `upload` rides `releases upload` (`.apk` → `apks.upload`, slice [#330](https://github.com/PollyGlot/google-play-cli/issues/330) / [ADR-0036](adr/0036-apk-upload-rides-releases-upload.md)); `list` + `addexternallyhosted` excluded |
| `inappproducts` (legacy IAP) | 9 | 🔵 | [#51](https://github.com/PollyGlot/google-play-cli/issues/51) |
| `monetization.subscriptions` (+`basePlans`+`offers`) | 24 | 🔵 | [#51](https://github.com/PollyGlot/google-play-cli/issues/51) (post-v1) |
| `monetization.onetimeproducts` (+`purchaseOptions`+`offers`) | 17 | 🔵 | folded into PRD [#51](https://github.com/PollyGlot/google-play-cli/issues/51) (was [#293](https://github.com/PollyGlot/google-play-cli/issues/293), 2026-07-08) — legacy-vs-v2 decided at #51's grilling |
| `monetization.convertRegionPrices` | 1 | 🔵 | folded into PRD [#51](https://github.com/PollyGlot/google-play-cli/issues/51) (was [#293](https://github.com/PollyGlot/google-play-cli/issues/293)) |
| `externaltransactions` | 3 | 🔴 | [#295](https://github.com/PollyGlot/google-play-cli/issues/295) **parked** (2026-07-08) — alternative billing / DMA reporting, niche |
| `generatedapks` | 2 | ✅ | `releases generated` (list/download) — [ADR-0034](adr/0034-generated-apks-binary-download-to-file.md), PRD [#299](https://github.com/PollyGlot/google-play-cli/issues/299) |
| `systemapks.variants` | 4 | 🔴 | [#296](https://github.com/PollyGlot/google-play-cli/issues/296) (parked — niche OEM/preload) |
| `purchases.voidedpurchases.list` | 1 | 🔵 | [#346](https://github.com/PollyGlot/google-play-cli/issues/346) (parked — refund/chargeback feed, deferred by [ADR-0031](adr/0031-orders-commerce-reads-and-gated-refund.md)) |
| `purchases.products` / `productsv2` | 4 | ⚫️ | runtime — token verification ([ADR-0026](adr/0026-maximal-admin-api-coverage.md)) |
| `purchases.subscriptions` / `subscriptionsv2` | 10 | ⚫️ | runtime — token verification (ADR-0026) |

## `playdeveloperreporting` v1beta1 — 21 methods

| Surface | Methods | State | gplay / issue |
|---|---|---|---|
| `vitals.*` (crashrate, anrrate, errors, slowstart, slowrendering, wakeups, wakelocks, lmk) | 18 | ✅ | `vitals` ([#49](https://github.com/PollyGlot/google-play-cli/issues/49)) |
| `anomalies.list` | 1 | ✅ | `vitals anomalies` |
| `apps.search` | 1 | 🔵 | [#347](https://github.com/PollyGlot/google-play-cli/issues/347) — server-authoritative `apps list` vs local registry ([ADR-0027](adr/0027-vitals-second-service-scope-readonly.md)) |
| `apps.fetchReleaseFilterOptions` | 1 | 🔴 | [#348](https://github.com/PollyGlot/google-play-cli/issues/348) (parked — minor helper, fold into `vitals` if release-keyed filters are needed) |

## `playcustomapp` v1 — 1 method

| Surface | Methods | State | gplay / issue |
|---|---|---|---|
| `accounts.customApps.create` | 1 | ✅ | `customapps create` — [ADR-0032](adr/0032-custom-apps-account-axis-gated-creation.md), PRD [#242](https://github.com/PollyGlot/google-play-cli/issues/242) (PR [#303](https://github.com/PollyGlot/google-play-cli/pull/303)) |

## `gamesConfiguration` v1configuration — 10 methods

| Surface | Methods | State | gplay / issue |
|---|---|---|---|
| `achievementConfigurations.*` (list/get/insert/update/delete) | 5 | ✅ | `games achievements list/view/create/update/delete` — [ADR-0033](adr/0033-games-services-configuration-draft-crud.md), PRD [#241](https://github.com/PollyGlot/google-play-cli/issues/241) (slices [#286](https://github.com/PollyGlot/google-play-cli/issues/286)/[#287](https://github.com/PollyGlot/google-play-cli/issues/287)) |
| `leaderboardConfigurations.*` (list/get/insert/update/delete) | 5 | ✅ | `games leaderboards list/view/create/update/delete` — [ADR-0033](adr/0033-games-services-configuration-draft-crud.md), PRD [#241](https://github.com/PollyGlot/google-play-cli/issues/241) (slice [#288](https://github.com/PollyGlot/google-play-cli/issues/288)) |

> The Play Games Services **runtime** API (player-facing achievement/leaderboard
> writes) is excluded by nature; only the *configuration* (publishing) surface
> above is in scope.

---

## Excluded by nature (never wrapped)

Runtime APIs — an ephemeral token is minted on-device and consumed server-side,
so they are structurally unusable from a terminal or agent:

- **Play Integrity API** (its own API; not in any snapshot here).
- `androidpublisher.purchases.products` / `productsv2` / `subscriptions` /
  `subscriptionsv2` (14 methods) — server-side purchase-token verification.

A one-off *debug* read for a dispute could exist later as an explicitly-scoped
diagnostic, outside the coverage sweep (ADR-0026).

## Maintenance

- Update the relevant row (and the Headline tally) in the **same PR** that ships
  a slice — a row flipping ✅ is part of the change, not a follow-up.
- When the Discovery snapshot is bumped, re-run the `jq` query above and diff the
  method list against this file; any new method that is neither ✅/🟡/🔵/⚫️ is a
  fresh blind spot → file a triage issue (like #293–#296).
