# Triage labels

Les skills parlent en termes de cinq rôles canoniques de triage. Cette
table mappe ces rôles vers les labels réels du tracker.

## Rôles d'état (un seul par issue)

| Rôle canonique     | Label dans ce repo  | Signification                                       |
| ------------------ | ------------------- | --------------------------------------------------- |
| `needs-triage`     | `needs-triage`      | Maintainer doit évaluer                              |
| `needs-info`       | `needs-info`        | En attente d'info du reporter                       |
| `ready-for-agent`  | `ready-for-agent`   | Fully specified, prêt pour un agent AFK             |
| `ready-for-human`  | _(pas encore créé)_ | Demande implémentation humaine — créer à la demande |
| `wontfix`          | `wontfix`           | Ne sera pas actionné                                |

## Rôles de catégorie (un seul par issue)

| Rôle canonique  | Label dans ce repo |
| --------------- | ------------------ |
| `bug`           | `bug`              |
| `enhancement`   | `enhancement`      |

## Labels de type d'issue (un seul par issue — local à ce projet)

Au-delà des rôles canoniques, ce projet maintient une taxonomie par
**type d'issue** pour distinguer PRDs, slices, refacto d'archi et
parking. Voir [issue-tracker.md](issue-tracker.md) pour les conventions
complètes.

| Label          | Quand l'appliquer                                                |
| -------------- | ---------------------------------------------------------------- |
| `type:prd`     | PRD produit par `/to-prd` ou ses équivalents                     |
| `type:slice`   | Sous-issue d'un PRD, produite par `/to-issues`                   |
| `type:arch`    | Refacto / décision d'archi isolée (titre `[arch] ...`)            |
| `type:parking` | Idée gardée hors MVP (titre `Parking: ...`)                       |

## Labels d'area (un seul par issue)

`area:auth`, `area:apps`, `area:releases`, `area:tracks`, `area:reviews`.

## Labels de priorité (un seul par issue)

`priority:high`, `priority:medium`, `priority:low`.

## Note pour `/triage`

L'application d'un label d'état (`ready-for-agent`, etc.) ne dispense
PAS de poser aussi `type:*`, `area:*` et `priority:*` si manquants.
