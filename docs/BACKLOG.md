# Backlog — surfaces API reportées après MVP

Ce fichier liste les parties de la Google Play Developer API (et APIs liées) **délibérément reportées** au-delà du MVP de `gplay`. Chaque entrée note ce que la surface permet et pourquoi elle est différée. Ce n'est ni un glossaire (voir `CONTEXT.md`) ni une décision tranchée (voir `docs/adr/`) — juste un registre du scope.

> **Mise à jour 2026-05-31 — quatre surfaces ne sont plus « reportées ».**
> Elles ont été promues en **PRD planifiés** (Route 1.x — voir
> [ROADMAP.md](ROADMAP.md)) : Listings textuels → [#50](https://github.com/PollyGlot/google-play-cli/issues/50),
> Vitals crashes/ANR + mappings ProGuard → [#49](https://github.com/PollyGlot/google-play-cli/issues/49),
> Subscriptions v2 & IAP one-shot → [#51](https://github.com/PollyGlot/google-play-cli/issues/51),
> Historique reviews >7j (CSV GCS) → [#94](https://github.com/PollyGlot/google-play-cli/issues/94).
> Leurs sections ci-dessous sont conservées comme rationnel d'origine. Tout le
> reste du fichier est toujours hors scope.

## MVP (rappel, in-scope)

- Auth — service account → OAuth2 access token
- `apps list` / `apps info`
- `releases upload` (AAB)
- `tracks list` / `tracks promote` / `tracks rollout`
- `reviews list` / `reviews reply`

Tout ce qui suit est **hors MVP**.

---

## Publication (Edits API)

### Listings textuels — `edits.listings`
Titre, description courte, description longue, vidéo YouTube, par locale.
**Pourquoi plus tard :** remplace `fastlane supply` côté texte, utile mais pas bloquant pour un CI qui ne fait qu'uploader des builds. Souvent piloté par un repo de metadata canonique séparé du repo de code, à concevoir une fois le MVP stable.

### Images de store — `edits.images`
Icône, feature graphic, screenshots phone/tablet/TV/Wear/Chromebook, promo graphics.
**Pourquoi plus tard :** workflow complexe (resize, slots par locale et par form factor), gros morceau à part entière. Souvent géré hors CI (designers).

### Mappings ProGuard/R8 — `edits.deobfuscationfiles`
Upload du fichier `mapping.txt` pour dé-obfusquer les crashes côté Play Console.
**Pourquoi plus tard :** crucial dès qu'on regarde les crashes — donc à coupler avec vitals. Sera ajouté quand on attaque vitals.

### Fichiers OBB / expansion — `edits.expansionfiles`
Mécanisme legacy pour distribuer >150 MB d'assets hors APK.
**Pourquoi plus tard :** legacy, remplacé par Play Asset Delivery. À ne faire que sur demande explicite.

### APK legacy — `edits.apks`
Upload d'APK au lieu d'AAB.
**Pourquoi plus tard :** Google n'accepte plus de nouveaux APK pour nouvelles apps depuis 2021. AAB-only par défaut, APK ajouté seulement si un user le réclame.

### Testers (groupes internes) — `edits.testers`
Gestion des emails de testeurs sur tracks closed.
**Pourquoi plus tard :** workflow secondaire, la plupart des équipes gèrent ça à la main dans Play Console une fois.

### Détails app — `edits.details`
Langue par défaut, email/téléphone/site contact.
**Pourquoi plus tard :** champs très peu modifiés une fois set.

### Disponibilité pays — `edits.countryavailability`
Liste des pays où l'app est dispo.
**Pourquoi plus tard :** changement rare, géré dans la console.

### App Recovery — `edits.apprecovery`
Rollback ciblé d'un release vers une version précédente pour des users impactés.
**Pourquoi plus tard :** incident response, niche. À considérer si on fait un module "incident" complet.

### Création / gestion de custom closed tracks — `tracks.create` + testers
Créer des tracks closed nommés (`qa-team`, `external-beta`...) et y gérer la liste de testeurs.
**Pourquoi plus tard :** le MVP accepte `--track <any-string>` en passthrough donc utiliser un track existant marche déjà. La *création* d'un nouveau closed track et la gestion fine des testeurs sont reportées.

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

### Snapshot Discovery doc offline (`docs/discovery/androidpublisher_v3.json`)
Google publie un Discovery doc machine-readable pour l'API. L'embarquer dans le repo accélère le lookup d'endpoints pour les agents (validation de flags, génération de skeleton, etc.) sans hit réseau.
**Pourquoi plus tard :** marginal au MVP. Utile dès qu'on commence à étendre la couverture API et que les agents font du code-gen contre le schéma.
