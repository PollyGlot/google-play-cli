# Roadmap

Vue d'ensemble de la structure des issues du projet. Mise à jour à chaque
nouveau PRD ou nouvelle décomposition via `/to-issues`.

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

## Mode de livraison — `0.x` rolling, gel 1.0 différé

**Décision (2026-06-01).** Pas de gel 1.0 à court terme : on reste en `0.x` et on
**shippe large** (plusieurs features/correctifs par mineure, breaking changes
permis — SemVer `0.x`, [ADR-0008](adr/0008-release-pipeline.md)). La politique de
stabilité [ADR-0010](adr/0010-versioning-public-contract-and-ga.md) reste valable
mais **en sommeil**. Le trigger de la checklist de durcissement
[#98](https://github.com/PollyGlot/google-play-cli/issues/98) est franchi, gardé
en **attente délibérée** : on continue à shipper l'outillage interne et la surface
additive avant de décider du gel.

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

## Thème actif — Durcissement interne & outillage agent

First-release readiness livré, le thème bascule vers le **durcissement interne**
plutôt que l'élargissement de surface : petits chantiers à haute confiance qui
réduisent la dette et outillent les agents.

| Ordre | # | Area | Sujet | Note |
|---|---|---|---|---|
| ✅ | [#93](https://github.com/PollyGlot/google-play-cli/issues/93) | arch | Extraire la machinery partagée des `… list` (column registry + render + `resolveColumns`) | **Livré** (#148, [ADR-0018](adr/0018-shared-list-table-machinery.md)) — débloque les `team users/grants list` de #147 |
| 2 | [#52](https://github.com/PollyGlot/google-play-cli/issues/52) | tooling | Snapshot offline du Discovery doc `androidpublisher_v3.json` (+ target de regen) | Levier code-gen agent ; le trigger « surface > ~10 commandes » est franchi |
| ✅ | [#176](https://github.com/PollyGlot/google-play-cli/issues/176) | auth | Surface invalid-credential errors from `EnsureAccount` (absent vs invalide) | **Livré** (#182, slices #177–#180, [ADR-0020](adr/0020-resolution-error-surfacing.md)) — `breaking-change` : exit code d'`auth status` |
| ✅ | [#53](https://github.com/PollyGlot/google-play-cli/issues/53) | skills | Bootstrap repo `google-play-cli-skills` + roster comprehensive (9 skills, toute la surface livrée) | **Livré** — [`PollyGlot/google-play-cli-skills`](https://github.com/PollyGlot/google-play-cli-skills) public (slices #183–#192, [ADR-0021](adr/0021-companion-skills-repo.md)) |
| 3 | [#206](https://github.com/PollyGlot/google-play-cli/issues/206) | platform | PRD Operational hardening — install vérifié, runtime borné, autorité imposable par l'environnement | Issu de l'audit archi/sécurité (2026-06-10) ; décomposé en 11 slices [#207–#217](https://github.com/PollyGlot/google-play-cli/issues/206) — P0 : #207–#209 ; #215 bloquée par #209 |

## Ensuite — surfaces additives (post first-release)

Planifiées, mais derrière le thème actif. Toujours en `0.x`, sans attendre de
gel.

> **Politique de couverture ([ADR-0026](adr/0026-maximal-admin-api-coverage.md), 2026-06-13) :**
> toute **Admin API** Play est in scope en totalité (Android Publisher complet,
> Reporting API, Games Services Publishing, Custom Apps). Seules les APIs
> *runtime* (Play Integrity, vérification d'achats temps réel) restent exclues,
> par nature. Les nouvelles surfaces shippent `[experimental]` d'abord
> ([ADR-0010](adr/0010-versioning-public-contract-and-ga.md)).

| Ordre | # | Area | Sujet | Note |
|---|---|---|---|---|
| 1 | [#49](https://github.com/PollyGlot/google-play-cli/issues/49) | vitals | Crashes/ANR (Reporting API) + mappings ProGuard/R8 | Observabilité **post-lancement** — débloque les dashboards agent/Cowork |
| 2 | [#51](https://github.com/PollyGlot/google-play-cli/issues/51) | monetization | Subscriptions v2 + IAP one-shot + sync RevenueCat | **Post-v1** (confirmé) |
| 3 | [#94](https://github.com/PollyGlot/google-play-cli/issues/94) | reviews | Historique reviews >7j via CSV reports GCS | Spike d'investigation d'abord |
| 4 | [#147](https://github.com/PollyGlot/google-play-cli/issues/147) | team | Team management — users & grants (permissions du compte développeur) | PRD durci (ADR-0015/0016/0017) ; décomposé via `/to-issues` en 9 slices #149–#157 |
| 5 | [#243](https://github.com/PollyGlot/google-play-cli/issues/243) | releases+ | Publisher long tail — internal app sharing, app recovery, device tier configs, expansion files | **PRD draft** (ADR-0026) — à griller puis `/to-issues` |
| 5b | [#245](https://github.com/PollyGlot/google-play-cli/issues/245) | orders | Orders — lookup & refund (`orders.get/batchget/refund`) | **PRD draft** (ADR-0026) — splitté de #243 : seul bloc money-touching, grilling write-safety dédié |
| 6 | [#241](https://github.com/PollyGlot/google-play-cli/issues/241) | games | Games Services configuration (`gamesConfiguration`) — achievements & leaderboards | **PRD draft** (ADR-0026) — API séparée, à griller |
| 7 | [#242](https://github.com/PollyGlot/google-play-cli/issues/242) | customapps | Custom apps — publication privée managed Google Play (`playcustomapp`) | **PRD draft** (ADR-0026) — seule API qui crée une app |
| — | [#118](https://github.com/PollyGlot/google-play-cli/issues/118) | releases | APK upload (`edits.apks`) | Apps **existantes** only — hors thème first-release (AAB obligatoire pour les nouvelles apps depuis 2021) |

Les docs utilisateur ([#116](https://github.com/PollyGlot/google-play-cli/issues/116))
avancent en parallèle, sans dépendre du gel 1.0.

## Parking (post-MVP)

Idées trackées pour visibilité, **pas** planifiées. Voir
[BACKLOG.md](BACKLOG.md) pour le rationnel d'exclusion.

| # | Sujet | Pourquoi parked |
|---|---|---|
| [#38](https://github.com/PollyGlot/google-play-cli/issues/38) | [arch] Renderer interface (contredit ADR-0005) | Décision à re-litiguer si besoin |
| [#48](https://github.com/PollyGlot/google-play-cli/issues/48) | `gplay edits begin/commit/discard` (mode explicit) | Implicit mode suffit pour MVP |

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
