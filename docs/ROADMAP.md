# Roadmap

Vue d'ensemble de la structure des issues du projet. Mise à jour à chaque
nouveau PRD ou nouvelle décomposition via `/to-issues`.

> **Couverture API.** Pour l'état method-level (« a-t-on tout couvert ? »), voir
> la matrice [COVERAGE.md](COVERAGE.md) — chaque méthode des 4 APIs admin mappée
> à livré / planifié / exclu.

> **Versioning :** piloté de bout en bout par release-please / SemVer depuis les
> commits conventionnels. **Pas de milestone versionné** — le scope se suit par
> issues PRD + labels (`type:*`, `area:*`), la version par les commits. Voir
> [ADR-0010](adr/0010-versioning-public-contract-and-ga.md).

## Types d'issues

| Type | Label | Convention de titre | Rôle |
|---|---|---|---|
| **PRD** | `type:prd` | `PRD: <area>` ou `PRD — <area>` | Spec complète d'un area, décomposable via `/to-issues` |
| **Slice** | `type:slice` | libre (préfixée par l'area, ex: `Apps: gplay apps list`) | Sous-issue d'un PRD, tracer-bullet vertical |
| **Arch** | `type:arch` | `[arch] ...` | Refactoring / décision d'architecture isolée |
| **Parking** | `type:parking` | `Parking: ...` | Idée gardée pour plus tard, hors MVP |

Un PRD reste ouvert tant qu'au moins une de ses slices est ouverte. À la
clôture, ses slices doivent toutes être closed.

## État actuel — MVP livré ✅

Toute la surface MVP est implémentée et mergée dans `main` :

| Area | Commandes | PRD |
|---|---|---|
| auth | login / logout / status / list / doctor | [#2](https://github.com/PollyGlot/google-play-cli/issues/2) ✅ |
| apps | add / list / view / details / remove + init | [#3](https://github.com/PollyGlot/google-play-cli/issues/3) ✅ |
| releases | upload / promote / rollout / list | [#4](https://github.com/PollyGlot/google-play-cli/issues/4) ✅ |
| tracks | list / view / create / availability | [#5](https://github.com/PollyGlot/google-play-cli/issues/5) ✅ |
| reviews | list / reply | [#6](https://github.com/PollyGlot/google-play-cli/issues/6) ✅ |
| output | dispatcher TTY-aware + markdown | [#26](https://github.com/PollyGlot/google-play-cli/issues/26) ✅ |

## Mode de livraison — `1.0` GA, contrat public en vigueur

**Décision (2026-08-01, [ADR-0042](adr/0042-one-zero-ga-and-stability-label-mechanism.md)).**
La `1.0.0` est coupée : le contrat public de
[ADR-0010](adr/0010-versioning-public-contract-and-ga.md) n'est plus en sommeil,
il **s'applique** à toute commande non étiquetée `[experimental]` — noms de
commandes/flags, sémantique, exit codes, schéma de config, précédence Account,
garantie passthrough du `--output json`. Un breaking sur cette surface = bump
majeur.

Le gel est **granulaire** : `kernel.Experimental` pose l'étiquette à
l'enregistrement (`cmd/gplay/main.go`), et
`TestStabilityRegistry_pinsPublicContract` échoue sur toute feuille non
classée — une commande neuve ne peut plus rejoindre le contrat par omission.
Ship `[experimental]` à la 1.0 : `schema`, `orders`, `subscriptions`, `iap`,
`games`, `recovery`, `device-tiers`, `customapps`, `releases sharing` /
`expansion-files` / `generated`, `reviews history`.

> **Historique.** Décision précédente (2026-06-01) : rester en `0.x` et shipper
> large, gel différé. Elle tenait tant que la surface bougeait ; ce qui reste au
> backlog est désormais **additif** (nouveaux namespaces), donc absorbable en
> `1.x` sans majeure. La checklist [#98](https://github.com/PollyGlot/google-play-cli/issues/98)
> n'est pas cochée intégralement — le dogfooding CI externe manque, et
> l'étiquetage par commande est la mitigation assumée (ADR-0042 §« What we
> lose »).

**Ce que ça change au quotidien :** un `feat`/`fix` sur une commande gelée doit
rester rétro-compatible ; s'il ne peut pas l'être, c'est un `!` et une majeure.
Sur une commande `[experimental]`, rien ne change. Retirer l'étiquette
(graduation) est additif → mineure.

## Thème livré ✅ — First-release readiness

**North star near-term :** *gplay prépare et publie une app de bout en bout, de
zéro à sa première release de production.* Toute la prépa d'une première mise en
ligne est **livrée** — les 6 surfaces ci-dessous sont shippées et mergées en
`0.x` :

| # | Area | Surface API | État |
|---|---|---|---|
| ✅ | metadata | Listings textuels (`edits.listings`) | Livré (slices #101–#107) |
| ✅ | metadata | Images et screenshots (`edits.images`) — grillé ([ADR-0013](adr/0013-image-slot-reconciliation.md)) → 6 slices #130–#135 | Livré (slices #130–#135) |
| ✅ | apps | App details — read + set (`edits.details`) | Livré (slices #126/#127) |
| ✅ | tracks | Country availability — read-only (`edits.countryavailability`) — splittée de #113 ([ADR-0012](adr/0012-app-details-writable-availability-readonly.md)) | Livré (slice #128) |
| ✅ | compliance | Data safety (`applications.dataSafety`) — grillé ([ADR-0014](adr/0014-compliance-namespace-datasafety-write-only.md)) → 3 slices #140–#142 | Livré (slices #140–#142) |
| ✅ | tracks | Custom closed tracks + testers (`tracks.create` + `edits.testers`) — **dé-parké** du backlog → 4 slices #120–#123 | Livré (slices #120–#123) |

> ⚠️ **Mur dur.** Content rating, public cible, déclarations ads/news n'ont
> **aucun endpoint** dans l'API Google → restent manuels en console. À
> **documenter**, pas à automatiser. La promesse « prépa complète depuis le
> CLI » s'arrête là.

## Thème livré ✅ — Durcissement interne & outillage agent

First-release readiness livré, ce thème a durci l'interne plutôt que d'élargir la
surface : petits chantiers à haute confiance réduisant la dette et outillant les
agents. **Entièrement clos (02–10 juin 2026)** — install vérifié, runtime borné,
autorité imposable par l'environnement, introspection de schéma embarquée.

| Ordre | # | Area | Sujet | Note |
|---|---|---|---|---|
| ✅ | [#93](https://github.com/PollyGlot/google-play-cli/issues/93) | arch | Extraire la machinery partagée des `… list` (column registry + render + `resolveColumns`) | **Livré** (#148, [ADR-0018](adr/0018-shared-list-table-machinery.md)) — débloque les `team users/grants list` de #147 |
| ✅ | [#52](https://github.com/PollyGlot/google-play-cli/issues/52) | tooling | Snapshot offline du Discovery doc `androidpublisher_v3.json` (+ target de regen) | **Livré** (clos 2026-06-08) — Discovery snapshot + `gplay schema` embarqué (#199–#201) |
| ✅ | [#176](https://github.com/PollyGlot/google-play-cli/issues/176) | auth | Surface invalid-credential errors from `EnsureAccount` (absent vs invalide) | **Livré** (#182, slices #177–#180, [ADR-0020](adr/0020-resolution-error-surfacing.md)) — `breaking-change` : exit code d'`auth status` |
| ✅ | [#53](https://github.com/PollyGlot/google-play-cli/issues/53) | skills | Bootstrap repo `google-play-cli-skills` + roster comprehensive (9 skills, toute la surface livrée) | **Livré** — [`PollyGlot/google-play-cli-skills`](https://github.com/PollyGlot/google-play-cli-skills) public (slices #183–#192, [ADR-0021](adr/0021-companion-skills-repo.md)) |
| ✅ | [#206](https://github.com/PollyGlot/google-play-cli/issues/206) | platform | PRD Operational hardening — install vérifié, runtime borné, autorité imposable par l'environnement | **Livré** (clos 2026-06-10) — 11 slices #207–#217 (timeout/`--retry`, `GPLAY_READONLY`, JSON error envelope, sanitization) |

## Thème actif — Couverture admin maximale (ADR-0026)

Durcissement interne clos, le thème bascule vers la **couverture des admin APIs
restantes** : compléter la surface jusqu'à ce que toute opération admin Play soit
pilotable depuis le CLI. Le thème continue **après** le gel 1.0 : ce qui reste
est additif (nouveaux namespaces), donc livrable en `1.x` sans majeure — les
surfaces neuves arrivent `[experimental]` (ADR-0042). L'arc
*publier → durcir → observer* est **complété** — vitals
[#49](https://github.com/PollyGlot/google-play-cli/issues/49) livré (PR #263).
Les **games** (#241, CRUD achievements/leaderboards, ADR-0033) et les **orders**
(#245, lookup + refund gated, ADR-0031) sont désormais **livrées** ; le gros bloc
restant est la **monetization** (#51, subscriptions + IAP). Les **custom apps**
(#242) sont **livrées** (PR #303), tout comme le quick win read-only **generated
APKs** (#299, trié depuis #294, PR #305).

> **Politique de couverture ([ADR-0026](adr/0026-maximal-admin-api-coverage.md), 2026-06-13) :**
> toute **Admin API** Play est in scope en totalité (Android Publisher complet,
> Reporting API, Games Services Publishing, Custom Apps). Seules les APIs
> *runtime* (Play Integrity, vérification d'achats temps réel) restent exclues,
> par nature. Les nouvelles surfaces shippent `[experimental]` d'abord
> ([ADR-0010](adr/0010-versioning-public-contract-and-ga.md)).

| Ordre | # | Area | Sujet | Prio | Note |
|---|---|---|---|---|---|
| ✅ | [#245](https://github.com/PollyGlot/google-play-cli/issues/245) | orders | Orders — lookup & refund (`orders.get/batchget/refund`) | low | **Livré** — grillé ([ADR-0031](adr/0031-orders-commerce-reads-and-gated-refund.md)), `orders view <id>...` (single `orders.get` + batch `orders.batchget`) / `refund --confirm [--revoke]` (gated, money-moving), axe package, voided-purchases reporté. **3 slices** [#282](https://github.com/PollyGlot/google-play-cli/issues/282) (PR #314) + [#283](https://github.com/PollyGlot/google-play-cli/issues/283)/[#284](https://github.com/PollyGlot/google-play-cli/issues/284) |
| ✅ | [#241](https://github.com/PollyGlot/google-play-cli/issues/241) | games | Games Services configuration (`gamesConfiguration`) — achievements & leaderboards | low | **Livré** — grillé ([ADR-0033](adr/0033-games-services-configuration-draft-crud.md)), namespace `games`, axe application-ID, draft-only (ni publish ni images) ; CRUD complet `games achievements`/`games leaderboards` (list/view/create/update/delete) → 3 slices [#286](https://github.com/PollyGlot/google-play-cli/issues/286)–[#288](https://github.com/PollyGlot/google-play-cli/issues/288) |
| ✅ | [#242](https://github.com/PollyGlot/google-play-cli/issues/242) | customapps | Custom apps — publication privée managed Google Play (`playcustomapp`) | low | **Livré** (PR [#303](https://github.com/PollyGlot/google-play-cli/pull/303)) — grillé ([ADR-0032](adr/0032-custom-apps-account-axis-gated-creation.md)), axe compte, gate `--confirm`, create-only → slice [#285](https://github.com/PollyGlot/google-play-cli/issues/285) |
| 🔨 | [#51](https://github.com/PollyGlot/google-play-cli/issues/51) | monetization | Subscriptions v2 + IAP one-shot & one-time products v2 + sync RevenueCat | medium | **Entamé** — grillé le 2026-07-20 (6 slices [#367](https://github.com/PollyGlot/google-play-cli/issues/367)–[#372](https://github.com/PollyGlot/google-play-cli/issues/372) + PRD RevenueCat [#373](https://github.com/PollyGlot/google-play-cli/issues/373)) ; **slice #367 livrée** ([ADR-0041](adr/0041-declarative-monetization-catalog.md)) : `subscriptions pull`/`apply` déclaratifs, moteur de réconciliation partagé ; scope étendu le 2026-07-08 ([#293](https://github.com/PollyGlot/google-play-cli/issues/293) fusionné) |
| prêt | [#94](https://github.com/PollyGlot/google-play-cli/issues/94) | reviews | Historique reviews >7j via CSV reports GCS | low | **Spiké + grillé** ([ADR-0037](adr/0037-reviews-history-gcs-csv-reports.md), 2026-07-08) — `reviews history --month`, scope `devstorage.read_only`, CSV UTF-16 → 2 slices [#331](https://github.com/PollyGlot/google-play-cli/issues/331)/[#332](https://github.com/PollyGlot/google-play-cli/issues/332) |
| prêt | [#118](https://github.com/PollyGlot/google-play-cli/issues/118) | releases | APK upload (`edits.apks`) | low | **Grillé** ([ADR-0036](adr/0036-apk-upload-rides-releases-upload.md), 2026-07-08) — `releases upload` accepte `.apk` (auto-détection + `--format`) → slice [#330](https://github.com/PollyGlot/google-play-cli/issues/330) ; apps **existantes** only (AAB obligatoire pour les nouvelles depuis 2021) |
| ✅ | [#337](https://github.com/PollyGlot/google-play-cli/issues/337) | apps | App icon retrieval — accesseur ergonomique + frontière (no cache) | low | **Livré** (PR [#342](https://github.com/PollyGlot/google-play-cli/pull/342)) — grillé ([ADR-0038](adr/0038-app-icon-faithful-read-no-cache.md), 2026-07-10) — plomberie déjà là ; `apps view` porte `icon {url,sha256}` + `metadata images list --type` ; `sha256` = handle durable (jamais `Image.url`), scope `androidpublisher` **complet** non gaté `GPLAY_READONLY`, **zéro cache dans gplay** → 3 slices [#338](https://github.com/PollyGlot/google-play-cli/issues/338)/[#339](https://github.com/PollyGlot/google-play-cli/issues/339)/[#340](https://github.com/PollyGlot/google-play-cli/issues/340) |
| ✅ | [#49](https://github.com/PollyGlot/google-play-cli/issues/49) | vitals | Vitals (metric sets + errors + anomalies) — Reporting API, read-only | medium | **Livré** (PR [#263](https://github.com/PollyGlot/google-play-cli/issues/263)) — grillé ([ADR-0027](adr/0027-vitals-second-service-scope-readonly.md)), service `playdeveloperreporting`/v1beta1 (2ᵉ scope OAuth) ; 5 slices [#258](https://github.com/PollyGlot/google-play-cli/issues/258)–[#262](https://github.com/PollyGlot/google-play-cli/issues/262) |
| ✅ | [#250](https://github.com/PollyGlot/google-play-cli/issues/250) | releases | Mappings ProGuard/R8 (`edits.deobfuscationfiles`) — `upload --mapping` + `releases mappings upload` | medium | **Livré** (PR [#264](https://github.com/PollyGlot/google-play-cli/issues/264)) — splitté de #49 ([ADR-0027](adr/0027-vitals-second-service-scope-readonly.md)), Edit publisher |
| ✅ | [#243](https://github.com/PollyGlot/google-play-cli/issues/243) | releases+ | Publisher long tail — internal app sharing, app recovery, device tier configs, expansion files | medium | **Livré** (PR [#280](https://github.com/PollyGlot/google-play-cli/issues/280)) — grillé → 4 slices [#275](https://github.com/PollyGlot/google-play-cli/issues/275)–[#279](https://github.com/PollyGlot/google-play-cli/issues/279) ([ADR-0030](adr/0030-android-publisher-long-tail-surfaces.md)) |
| ✅ | [#299](https://github.com/PollyGlot/google-play-cli/issues/299) | releases | Generated APKs (`generatedapks`) — `releases generated list`/`download` les APKs que Play signe depuis un AAB | low | **Livré** (PR [#305](https://github.com/PollyGlot/google-play-cli/pull/305)) — grillé ([ADR-0034](adr/0034-generated-apks-binary-download-to-file.md), 1er download-to-file binaire) → 2 slices [#300](https://github.com/PollyGlot/google-play-cli/issues/300)/[#301](https://github.com/PollyGlot/google-play-cli/issues/301) ; reads Edit-free, verbe `download` |
| ✅ | [#147](https://github.com/PollyGlot/google-play-cli/issues/147) | team | Team management — users & grants | — | **Livré** (clos 2026-06-02) — 9 slices #149–#157 (ADR-0015/0016/0017) |

**Chantiers transverses (livrés ✅)** : convention de confirmation `✓` sur les
mutations ([#238](https://github.com/PollyGlot/google-play-cli/issues/238) +
[#247](https://github.com/PollyGlot/google-play-cli/issues/247), DESIGN §8, closes
2026-06-15), et le quick win dé-parké [#162](https://github.com/PollyGlot/google-play-cli/issues/162)
(`team users view`, débloqué par la clôture de #98) — livré via PRD
[#271](https://github.com/PollyGlot/google-play-cli/issues/271) / PR
[#274](https://github.com/PollyGlot/google-play-cli/issues/274). Le mode Edit
**explicite** ([#48](https://github.com/PollyGlot/google-play-cli/issues/48),
`gplay edits begin/commit/discard/status`, DESIGN §4) est lui aussi livré : un
Edit ouvert est épinglé dans `.gplay/edit-<package>.json` et réutilisé par les
commandes d'écriture suivantes.

## Parking (post-MVP)

Idées trackées pour visibilité, **pas** planifiées. Voir
[BACKLOG.md](BACKLOG.md) pour le rationnel d'exclusion.

| # | Sujet | Pourquoi parked |
|---|---|---|
| [#295](https://github.com/PollyGlot/google-play-cli/issues/295) | External transactions (alternative billing / DMA) | Niche-dans-la-niche (devs enrollés alternative billing only) ; parké 2026-07-08 |
| [#296](https://github.com/PollyGlot/google-play-cli/issues/296) | System APK variants (`systemapks`) | OEM/preload only |
| [#146](https://github.com/PollyGlot/google-play-cli/issues/146) | Distribution WinGet | En attente de demande |
| [#115](https://github.com/PollyGlot/google-play-cli/issues/115) | Réglementaire hors Developer API | Aucun endpoint — à documenter, pas à automatiser |

> [#38](https://github.com/PollyGlot/google-play-cli/issues/38) ([arch] Renderer
> interface) est **clos** (2026-07-08) : l'exploration demandée a été livrée par
> le refactor kernel (#40) — `output.Renderable` est exactement la forme
> payload-driven visée, ADR-0005 intact en dessous.

## Commandes utiles

```bash
# Tous les PRDs (ouverts ou fermés)
gh issue list --label type:prd --state all

# Slices ouvertes prêtes pour un agent
gh issue list --label type:slice --label ready-for-agent

# Parking (rappel des idées post-MVP)
gh issue list --label type:parking --state open

# Issues d'un area donné (PRD + slices ensemble)
gh issue list --label area:tracks --state open
```
