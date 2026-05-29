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
| apps | add / list / info / remove + init | [#3](https://github.com/PollyGlot/google-play-cli/issues/3) ✅ |
| releases | upload / promote / rollout / list | [#4](https://github.com/PollyGlot/google-play-cli/issues/4) ✅ |
| tracks | list / status (read-only) | [#5](https://github.com/PollyGlot/google-play-cli/issues/5) ✅ |
| reviews | list / reply | [#6](https://github.com/PollyGlot/google-play-cli/issues/6) ✅ |
| output | dispatcher TTY-aware + markdown | [#26](https://github.com/PollyGlot/google-play-cli/issues/26) ✅ |

Statut produit : **public preview (pre-1.0)**. La `v0.2.0` (reviews) sort dès le
merge de la release PR release-please, puis on enchaîne sur la route v1.0.

## Route vers v1.0 (GA)

La 1.0 gèle la surface MVP derrière un *Public contract* (voir
[ADR-0010](adr/0010-versioning-public-contract-and-ga.md) et `CONTEXT.md` :
*Public contract*, *Stability label*, *Public preview / GA*). Checklist,
décisions et audit du vocabulaire de verbes dans
**[#98](https://github.com/PollyGlot/google-play-cli/issues/98)** : stability
labels, dogfooding sur une vraie app, docs `concepts/`, guide `migrate-to-1-0`,
set de skills v1 (#53).

## Parking (post-MVP)

Idées trackées pour visibilité, **pas** planifiées. Voir
[BACKLOG.md](BACKLOG.md) pour le rationnel d'exclusion.

| # | Sujet | Pourquoi parked |
|---|---|---|
| [#38](https://github.com/PollyGlot/google-play-cli/issues/38) | [arch] Renderer interface (contredit ADR-0005) | Décision à re-litiguer si besoin |
| [#48](https://github.com/PollyGlot/google-play-cli/issues/48) | `gplay edits begin/commit/discard` (mode explicit) | Implicit mode suffit pour MVP |
| [#49](https://github.com/PollyGlot/google-play-cli/issues/49) | PRD Vitals (crashes/ANR + ProGuard mappings) | API séparée, sous-module entier |
| [#50](https://github.com/PollyGlot/google-play-cli/issues/50) | PRD Metadata listings (textes par locale) | Hors MVP |
| [#51](https://github.com/PollyGlot/google-play-cli/issues/51) | PRD Subscriptions & IAP (+ RevenueCat sync) | Hors MVP |
| [#52](https://github.com/PollyGlot/google-play-cli/issues/52) | Snapshot Discovery doc | Doc-only |
| [#53](https://github.com/PollyGlot/google-play-cli/issues/53) | Bootstrap `google-play-cli-skills` repo | Sibling repo — set de skills v1 du lancement GA |
| [#55](https://github.com/PollyGlot/google-play-cli/issues/55) | Migration vers `androidpublisher/v3` Go client officiel | Refacto interne |

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
