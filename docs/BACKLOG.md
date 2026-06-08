# Backlog — surfaces API reportées après MVP

Ce fichier liste les parties de la Google Play Developer API (et APIs liées) **délibérément reportées** au-delà du MVP de `gplay`. Chaque entrée note ce que la surface permet et pourquoi elle est différée. Ce n'est ni un glossaire (voir `CONTEXT.md`) ni une décision tranchée (voir `docs/adr/`) — juste un registre du scope.

> Plusieurs surfaces listées ici ont depuis été **livrées** (marquées ✅) ou
> promues en **PRD planifié** ; leurs sections sont conservées pour le rationnel
> d'origine. L'état planifié-vs-livré à jour vit dans [ROADMAP.md](ROADMAP.md) —
> ici, seul ce qui n'est ni ✅ ni 🔼 est encore hors scope.

## MVP (rappel, in-scope)

- Auth — service account → OAuth2 access token
- `apps list` / `apps view`
- `releases upload` (AAB)
- `tracks list` / `tracks promote` / `tracks rollout`
- `reviews list` / `reviews reply`

Tout ce qui suit est **hors MVP**.

---

## Publication (Edits API)

### Listings textuels — `edits.listings` — ✅ **livré** (PRD [#50](https://github.com/PollyGlot/google-play-cli/issues/50), slices #101–#107)
Titre, description courte, description longue, vidéo YouTube, par locale.
**Statut :** `gplay metadata list/pull/validate/apply` (sync additif, [ADR-0011](adr/0011-metadata-apply-sync-model.md)).

### Images de store — `edits.images` — ✅ **livré** (PRD [#112](https://github.com/PollyGlot/google-play-cli/issues/112), slices #130–#135)
Icône, feature graphic, screenshots phone/tablet 7"/tablet 10"/TV/Wear, promo graphics — par locale.
**Statut :** `gplay metadata images list/pull/validate/apply` (+`--prune`) — sync calqué sur [ADR-0011](adr/0011-metadata-apply-sync-model.md) + identité par `sha256` ([ADR-0013](adr/0013-image-slot-reconciliation.md)).
**Hors scope retenu :** (a) **Chromebook screenshots** — non exposé par l'énum `AppImageType` de `androidpublisher v3` (UI Play Console only) ; à ajouter si/quand Google l'expose. (b) **`metadata images clear`** (vider entièrement un slot via `deleteall`) — seul verbe sans équivalent déclaratif, repoussé (ADR-0013 §3). (c) Resize/composition/framing d'images (downstream tooling).

### Mappings ProGuard/R8 — `edits.deobfuscationfiles`
Upload du fichier `mapping.txt` pour dé-obfusquer les crashes côté Play Console.
**Pourquoi plus tard :** crucial dès qu'on regarde les crashes — donc à coupler avec vitals. Sera ajouté quand on attaque vitals.

### Fichiers OBB / expansion — `edits.expansionfiles`
Mécanisme legacy pour distribuer >150 MB d'assets hors APK.
**Pourquoi plus tard :** legacy, remplacé par Play Asset Delivery. À ne faire que sur demande explicite.

### APK legacy — `edits.apks` — 🔼 **PRD [#118](https://github.com/PollyGlot/google-play-cli/issues/118) (plus tard, hors first-release)**
Upload d'APK au lieu d'AAB.
> **Promu en PRD planifié mais déprioritisé** (2026-06-01) — apps existantes only, voir [ROADMAP.md](ROADMAP.md) « Ensuite ».
**Pourquoi plus tard :** Google n'accepte plus de nouveaux APK pour nouvelles apps depuis 2021. AAB-only par défaut, APK ajouté seulement si un user le réclame.

### Testers (groupes internes) — `edits.testers` — ✅ **livré** (PRD [#117](https://github.com/PollyGlot/google-play-cli/issues/117), slices #120–#123)
Gestion des emails de testeurs sur tracks closed.
**Statut :** `gplay testers list/set` (test fermé désormais obligatoire avant la 1ère prod). Couplé aux custom closed tracks ci-dessous.

### Détails app — `edits.details` — ✅ **livré** (PRD [#113](https://github.com/PollyGlot/google-play-cli/issues/113))
Langue par défaut, email/téléphone/site contact.
**Statut :** `gplay apps details view` / `apps details set` (read + set) — [#126](https://github.com/PollyGlot/google-play-cli/issues/126)/[#127](https://github.com/PollyGlot/google-play-cli/issues/127).

### Disponibilité pays — `edits.countryavailability` — ✅ **livré** (PRD [#113](https://github.com/PollyGlot/google-play-cli/issues/113), read-only)
Liste des pays où l'app est dispo.
**Statut :** `gplay tracks availability view` (read-only, [ADR-0012](adr/0012-app-details-writable-availability-readonly.md)) — [#128](https://github.com/PollyGlot/google-play-cli/issues/128).

### App Recovery — `edits.apprecovery`
Rollback ciblé d'un release vers une version précédente pour des users impactés.
**Pourquoi plus tard :** incident response, niche. À considérer si on fait un module "incident" complet.

### Création / gestion de custom closed tracks — `tracks.create` + testers — ✅ **livré** (PRD [#117](https://github.com/PollyGlot/google-play-cli/issues/117), slices #120–#123)
Créer des tracks closed nommés (`qa-team`, `external-beta`...) et y gérer la liste de testeurs.
**Statut :** `gplay tracks create` (+ `testers list/set`).

---

## Monetization

### IAP one-shot — `inappproducts`
CRUD des produits in-app non-abonnement.
**Pourquoi plus tard :** gros sous-domaine (CRUD complet + pricing par territoire). Sera un module à part entière post-MVP.

### Subscriptions v2 — `monetization.subscriptions` + `basePlans` + `offers`
Abonnements, base plans, offers, pricing par territoire.
**Pourquoi plus tard :** API très récente et complexe (3 niveaux imbriqués). Gros morceau à part entière, mérite son propre module et probablement sa propre paire de skills `gplay-subscription-management` + un skill de synchronisation avec RevenueCat.

### Vérification d'achats — `purchases.products` / `purchases.subscriptionsv2` / `purchases.voidedpurchases`
Validation côté serveur des tokens de purchase.
**Pourquoi plus tard :** c'est une API serveur, pas un usage CLI naturel. À n'exposer que si un cas concret le réclame (debug d'un litige par exemple).

---

## Vitals (Play Developer Reporting API — séparée)

### Crashes / ANR rates
`vitals.crashrate.query`, `vitals.anrrate.query`, `vitals.errors.reports`, `vitals.errors.counts`.
**Pourquoi plus tard :** API distincte (Reporting API ≠ Developer API), auth identique mais surface séparée. Bloc cohérent à attaquer en un module `gplay vitals`. Nécessite aussi l'upload des mappings (`deobfuscationfiles`) pour être utile.

### Slow start / slow rendering / wakeups / wakelocks
`vitals.slowstart`, `vitals.slowrendering`, `vitals.excessivewakeups`, `vitals.stuckbackgroundwakelocks`.
**Pourquoi plus tard :** même module vitals, métriques secondaires.

---

## Reviews — au-delà de la fenêtre 7 jours

### Import des CSV reports d'historique reviews
L'API `reviews.list` ne retourne que les 7 derniers jours. Pour l'historique long, Google expose des **CSV reports** dans un bucket GCS dédié (`gs://pubsite_prod_rev_<id>/reviews/`).
**Pourquoi plus tard :** demande l'auth GCS (autre scope OAuth) et un parseur CSV séparé. Utile pour analyse longitudinale, pas pour le flow CI standard.

---

## Auto-discovery & ergonomie

### Découverte réelle des apps via Cloud Resource Manager + IAM
Le MVP utilise un registre local (`~/.gplay/config.json`) parce qu'il n'y a pas d'`apps.list` dans la Developer API. Pour découvrir automatiquement les apps qu'un SA peut administrer, il faut interroger les Cloud Resource Manager / IAM APIs.
**Pourquoi plus tard :** alourdit le setup (scopes OAuth additionnels, permissions GCP), casse la promesse "donne juste le SA JSON". Faisable si quelqu'un en a vraiment besoin.

### `releases upload` resumable + retry intelligent
Upload AAB de 100+ MB. Google supporte l'upload résumable. Sur erreur réseau au milieu, on peut reprendre.
**Pourquoi plus tard :** le client Go officiel le gère déjà partiellement. À polir une fois MVP en production sur des AAB lourds.

---

## Autres surfaces

### Internal app sharing — `internalappsharingartifacts`
Upload d'un AAB/APK pour partage par lien (ne passe pas par un track).
**Pourquoi plus tard :** workflow QA spécifique, pas universel.

### Custom Apps / Managed Google Play — `customapps`
Distribution privée d'apps à des organisations.
**Pourquoi plus tard :** niche entreprise, hors usage standard.

### Play Integrity API
Vérification serveur de l'intégrité de l'install.
**Pourquoi plus tard :** **probablement non-goal** pour un CLI. C'est une API consommée par un backend en runtime, pas un outil d'administration. À reconfirmer si un cas pratique se présente.

### Play Asset Delivery (PAD)
Distribution d'assets dynamiques (install-time, fast-follow, on-demand).
**Pourquoi plus tard :** configuration côté projet Gradle, peu de surface CLI utile.

---

## Distribution & lifecycle

### Scoop / Chocolatey (Windows)
Manifest packaging pour Windows.
**Pourquoi plus tard :** binaire Windows déjà dispo via GitHub Releases + install script PowerShell. Scoop/Choco demandent un manifest à maintenir — à ajouter sur demande d'un user Windows.

### APT / RPM repos (Linux)
Distribution native via gestionnaires de paquets Linux.
**Pourquoi plus tard :** demande hosting de repo signé, plus lourd. `go install` et l'install script couvrent déjà Linux.

### Docker image
Image officielle pour CI containers (ex: `gplay-cli/gplay:latest`).
**Pourquoi plus tard :** Goreleaser peut le générer trivialement quand on en aura besoin. Pas urgent — la plupart des CI peuvent exécuter le binaire natif.

### `gplay self update`
Auto-update du binaire depuis GitHub Releases.
**Pourquoi plus tard :** `brew upgrade`, install script re-run, et `go install ...@latest` couvrent déjà les cas. Self-update ajoute une surface de sécurité (signature, checksum) sans gain UX significatif.

---

## Documentation & onboarding

### Tableau de migration Fastlane → gplay complet
Mapping détaillé `fastlane supply` ↔ `gplay`, options par options, avec exemples côte à côte.
**Pourquoi plus tard :** à écrire une fois qu'on a du retour de vrais migrateurs sur les pièges réels. Écrit en aveugle, le tableau sera incomplet.

### Exemples CI/CD multi-providers
`docs/CI_CD.md` ne couvre que GitHub Actions en MVP. Bitrise, GitLab CI, CircleCI, Jenkins à venir.
**Pourquoi plus tard :** patterns identiques modulo l'injection de secrets. Ajouter les exemples concrets quand un user d'un provider donné en a besoin.
