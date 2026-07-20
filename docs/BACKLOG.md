# Backlog — surfaces API reportées après MVP

Ce fichier liste les parties de la Google Play Developer API (et APIs liées) **pas encore livrées** par `gplay`. Chaque entrée note ce que la surface permet et son ordonnancement. Ce n'est ni un glossaire (voir `CONTEXT.md`) ni une décision tranchée (voir `docs/adr/`) — juste un registre du scope.

> **Changement de politique ([ADR-0026](adr/0026-maximal-admin-api-coverage.md), 2026-06-13).**
> Toute **Admin API** Play (opération du développeur sur son app) est désormais
> **in scope en totalité** — ce registre ordonne, il ne gatekeep plus. Les seules
> exclusions restantes sont **par nature** : les APIs *runtime* (Play Integrity,
> vérification d'achats temps réel), inutilisables depuis un terminal.

> Plusieurs surfaces listées ici ont depuis été **livrées** (marquées ✅) ou
> promues en **PRD planifié** ; leurs sections sont conservées pour le rationnel
> d'origine. L'état planifié-vs-livré à jour vit dans [ROADMAP.md](ROADMAP.md) —
> ici, seul ce qui n'est ni ✅ ni 🔼 est encore hors scope.

> **Couverture exhaustive (method-level).** Ce registre est thématique, pas
> exhaustif. La **matrice complète** des ~155 méthodes admin des 4 APIs (livré /
> planifié / exclu) vit dans [COVERAGE.md](COVERAGE.md) — c'est le garde-fou
> contre les angles morts.

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

### Mappings ProGuard/R8 — `edits.deobfuscationfiles` — ✅ **livré** ([#250](https://github.com/PollyGlot/google-play-cli/issues/250), éclaté du PRD vitals [#49](https://github.com/PollyGlot/google-play-cli/issues/49))
Upload du fichier `mapping.txt` pour dé-obfusquer les crashes côté Play vitals.
**Statut :** `gplay releases upload --mapping <mapping.txt>` (dans le même Edit que l'AAB) et `gplay releases mappings upload <mapping.txt> --version-code N` (après coup, pour une version déjà publiée). Surface sous `releases`, pas `vitals` : un Mapping est un upload d'Edit Android Publisher, pas une lecture du service Reporting ([ADR-0027](adr/0027-vitals-second-service-scope-readonly.md) / `CONTEXT.md` § Mapping). Types lus de l'énum Discovery (`proguard` / `nativeCode`).

### Fichiers OBB / expansion — `edits.expansionfiles` — ✅ **livré (`releases expansion-files`, slice [#278](https://github.com/PollyGlot/google-play-cli/issues/278) du PRD [#243](https://github.com/PollyGlot/google-play-cli/issues/243))**
Mécanisme legacy pour distribuer >150 MB d'assets hors APK.
**Statut :** livré `[experimental]` — `releases expansion-files upload/set/view` ([ADR-0030](adr/0030-android-publisher-long-tail-surfaces.md)), labelé legacy.

### APK legacy — `edits.apks` — 🔼 **PRD [#118](https://github.com/PollyGlot/google-play-cli/issues/118) grillé ([ADR-0036](adr/0036-apk-upload-rides-releases-upload.md)) → slice [#330](https://github.com/PollyGlot/google-play-cli/issues/330)**
Upload d'APK au lieu d'AAB.
> **Grillé le 2026-07-08** : pas de nouvelle commande — `releases upload` accepte `.apk` (auto-détection par extension + `--format`, la convention de `releases sharing upload`) ; `edits.apks.list` et `addexternallyhosted` exclus.
**Pourquoi plus tard :** Google n'accepte plus de nouveaux APK pour nouvelles apps depuis 2021 — apps existantes only, complétude/parité.

### Testers (groupes internes) — `edits.testers` — ✅ **livré** (PRD [#117](https://github.com/PollyGlot/google-play-cli/issues/117), slices #120–#123)
Gestion des emails de testeurs sur tracks closed.
**Statut :** `gplay testers list/set` (test fermé désormais obligatoire avant la 1ère prod). Couplé aux custom closed tracks ci-dessous.

### Détails app — `edits.details` — ✅ **livré** (PRD [#113](https://github.com/PollyGlot/google-play-cli/issues/113))
Langue par défaut, email/téléphone/site contact.
**Statut :** `gplay apps details view` / `apps details set` (read + set) — [#126](https://github.com/PollyGlot/google-play-cli/issues/126)/[#127](https://github.com/PollyGlot/google-play-cli/issues/127).

### Disponibilité pays — `edits.countryavailability` — ✅ **livré** (PRD [#113](https://github.com/PollyGlot/google-play-cli/issues/113), read-only)
Liste des pays où l'app est dispo.
**Statut :** `gplay tracks availability view` (read-only, [ADR-0012](adr/0012-app-details-writable-availability-readonly.md)) — [#128](https://github.com/PollyGlot/google-play-cli/issues/128).

### App Recovery — `apprecovery` — ✅ **livré (namespace `recovery`, slices [#277](https://github.com/PollyGlot/google-play-cli/issues/277)+[#279](https://github.com/PollyGlot/google-play-cli/issues/279) du PRD [#243](https://github.com/PollyGlot/google-play-cli/issues/243))**
Remédiation ciblée poussant les users impactés vers une version saine.
**Statut :** livré `[experimental]` — `recovery create/list/deploy/cancel/add-targeting` ([ADR-0030](adr/0030-android-publisher-long-tail-surfaces.md) ; verbes `deploy`/`cancel`/`add-targeting` admis sous ADR-0019 §2).

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

### Orders — `orders.get` / `orders.batchget` / `orders.refund` — ✅ **livré** (PRD [#245](https://github.com/PollyGlot/google-play-cli/issues/245), slices [#282](https://github.com/PollyGlot/google-play-cli/issues/282)–[#284](https://github.com/PollyGlot/google-play-cli/issues/284))
Lecture d'une commande par order ID (diagnostic support/litige) et refund (write money-moving).
**Statut :** livré `[experimental]` — grillé ([ADR-0031](adr/0031-orders-commerce-reads-and-gated-refund.md)) ; namespace `orders` sur l'axe package ; lecture `orders view <id>...` (variadic : 1→`get`, 2-1000→`batchget`, >1000→exit 2) ; `orders refund <id> --confirm [--revoke]` (exit-3 si flag absent, `MarkMutating`, pas de bulk, `--revoke` défaut false, refus spécifique >3 ans) ; permissions `CAN_VIEW_FINANCIAL_DATA` (lecture) / `CAN_MANAGE_ORDERS` (refund), jamais bundlées (ADR-0016).

### Vérification d'achats — `purchases.products` / `purchases.subscriptionsv2`
Validation côté serveur des tokens de purchase.
**Statut ([ADR-0026](adr/0026-maximal-admin-api-coverage.md)) :** exclu **par nature** du sweep de couverture — c'est une surface *runtime* (le backend valide des tokens en serving). Un read de *debug* ponctuel (litige) pourra exister plus tard comme diagnostic explicitement cadré, hors couverture.

**Cas limite — `purchases.voidedpurchases.list` :** endpoint de *liste* (achats annulés/remboursés, anti-fraude) qu'on interroge côté admin, sans token device — admin-leaning, pas runtime. **Tranché au grilling de #245 ([ADR-0031](adr/0031-orders-commerce-reads-and-gated-refund.md)) : in scope (admin) mais hors #245** — use case de réconciliation distinct du lookup d'order ; reporté à un futur PRD commerce (adjacent subscriptions/IAP [#51](https://github.com/PollyGlot/google-play-cli/issues/51)).

---

## Vitals (Play Developer Reporting API — séparée) — ✅ **livré** (PRD [#49](https://github.com/PollyGlot/google-play-cli/issues/49), PR #263)

> Module `gplay vitals` livré (crashes/ANR, errors, anomalies + les 7 metric sets) — grillé [ADR-0027](adr/0027-vitals-second-service-scope-readonly.md), slices #258–#262. Les mappings ProGuard/R8 (#250) ont suivi (PR #264). Les deux sous-sections ci-dessous sont conservées pour le rationnel d'origine.

### Crashes / ANR rates
`vitals.crashrate.query`, `vitals.anrrate.query`, `vitals.errors.reports`, `vitals.errors.counts`.
**Pourquoi plus tard :** API distincte (Reporting API ≠ Developer API), auth identique mais surface séparée. Bloc cohérent à attaquer en un module `gplay vitals`. Nécessite aussi l'upload des mappings (`deobfuscationfiles`) pour être utile.

### Slow start / slow rendering / wakeups / wakelocks
`vitals.slowstart`, `vitals.slowrendering`, `vitals.excessivewakeups`, `vitals.stuckbackgroundwakelocks`.
**Pourquoi plus tard :** même module vitals, métriques secondaires.

---

## Reviews — au-delà de la fenêtre 7 jours

### Import des CSV reports d'historique reviews — **PRD [#94](https://github.com/PollyGlot/google-play-cli/issues/94) spiké + grillé ([ADR-0037](adr/0037-reviews-history-gcs-csv-reports.md)) → slices [#331](https://github.com/PollyGlot/google-play-cli/issues/331)/[#332](https://github.com/PollyGlot/google-play-cli/issues/332)**
L'API `reviews.list` ne retourne que les 7 derniers jours. Pour l'historique long, Google expose des **CSV reports** dans un bucket GCS dédié (`gs://pubsite_prod_rev_<id>/reviews/`).
> **Spike résolu (2026-07-08)** : même service account + scope `devstorage.read_only` (pattern 2ᵉ service d'[ADR-0027](adr/0027-vitals-second-service-scope-readonly.md)), bucket dérivé du developer ID ([ADR-0015](adr/0015-developer-account-addressing-rides-on-account.md)) avec `--bucket` en override, CSV **UTF-16** transcodé. Surface : `reviews history --month`, sibling de `reviews list`.

---

## Auto-discovery & ergonomie

### Découverte réelle des apps via Cloud Resource Manager + IAM
Le MVP utilise un registre local (`~/.gplay/config.json`) parce qu'il n'y a pas d'`apps.list` dans la Developer API. Pour découvrir automatiquement les apps qu'un SA peut administrer, il faut interroger les Cloud Resource Manager / IAM APIs.
**Pourquoi plus tard :** alourdit le setup (scopes OAuth additionnels, permissions GCP), casse la promesse "donne juste le SA JSON". Faisable si quelqu'un en a vraiment besoin.
**Livré (2026-07-20) — `apps.search` via `apps accessible list`** ([#347](https://github.com/PollyGlot/google-play-cli/issues/347), [ADR-0039](adr/0039-accessible-apps-vs-local-registry.md)) : la **Play Developer Reporting API** (`playdeveloperreporting`, voir vitals [#49](https://github.com/PollyGlot/google-play-cli/issues/49), [ADR-0027](adr/0027-vitals-second-service-scope-readonly.md)) expose **`apps.search`** — énumération des apps accessibles au service account, *sans* Cloud Resource Manager ni scope GCP, juste le scope `playdeveloperreporting` déjà câblé pour vitals. Livré comme surface de **découverte** distincte (`apps accessible list`), **pas** un remplacement du registre local : les deux ensembles ne coïncident pas par nature (permissions Reporting vs Android Publisher distinctes, ADR-0039). Le registre local reste la source de vérité du working set. La piste lourde Cloud Resource Manager + IAM ci-dessus reste hors scope.

### `releases upload` resumable + retry intelligent
Upload AAB de 100+ MB. Google supporte l'upload résumable. Sur erreur réseau au milieu, on peut reprendre.
**Pourquoi plus tard :** le client Go officiel le gère déjà partiellement. À polir une fois MVP en production sur des AAB lourds.

---

## Autres surfaces

### Internal app sharing — `internalappsharingartifacts` — ✅ **livré (`releases sharing upload`, slice [#275](https://github.com/PollyGlot/google-play-cli/issues/275) du PRD [#243](https://github.com/PollyGlot/google-play-cli/issues/243))**
Upload d'un AAB/APK pour partage par lien (ne passe pas par un track).
**Statut :** livré `[experimental]` — `releases sharing upload` ([ADR-0030](adr/0030-android-publisher-long-tail-surfaces.md)).

### Device tier configs — `applications.deviceTierConfigs` — ✅ **livré (namespace `device-tiers`, slice [#276](https://github.com/PollyGlot/google-play-cli/issues/276) du PRD [#243](https://github.com/PollyGlot/google-play-cli/issues/243))**
Configs de targeting device pour la delivery par tiers (create/get/list).
**Statut :** livré `[experimental]` — `device-tiers create/view/list` ([ADR-0030](adr/0030-android-publisher-long-tail-surfaces.md)).

### APKs générés — `generatedapks` — ✅ **livré (`releases generated list/download`, slices [#300](https://github.com/PollyGlot/google-play-cli/issues/300)–[#301](https://github.com/PollyGlot/google-play-cli/issues/301) du PRD [#299](https://github.com/PollyGlot/google-play-cli/issues/299), PR [#305](https://github.com/PollyGlot/google-play-cli/pull/305))**
Lister puis télécharger les APK (split/standalone/universal + slices asset-pack/recovery) que Play génère et signe à partir d'un AAB uploadé — pour vérifier la signature, sideloader ou archiver l'artefact exact servi.
**Statut :** livré `[experimental]` ([ADR-0034](adr/0034-generated-apks-binary-download-to-file.md)) ; deux lectures **sans Edit** (scoping application, hors `/edits/`), sous `releases generated`. `list --version-code N` énumère ; `download <downloadId> --version-code N --dest PATH` streame les octets bruts vers un fichier (`--dest -` = stdout) — **premier geste binaire « download-to-file » du CLI**, d'où un verbe `download` neuf (ADR-0019) et un flag `--dest` distinct de `--output`.

### Custom apps / managed Google Play — `playcustomapp` — ✅ **livré (`customapps create`, slice [#285](https://github.com/PollyGlot/google-play-cli/issues/285) du PRD [#242](https://github.com/PollyGlot/google-play-cli/issues/242), PR [#303](https://github.com/PollyGlot/google-play-cli/pull/303))**
Distribution privée d'apps à des organisations — la **seule** API capable de créer une app.
**Statut :** livré `[experimental]` ([ADR-0032](adr/0032-custom-apps-account-axis-gated-creation.md)) ; API séparée (`playcustomapp`), namespace `customapps` sur l'**axe compte développeur** (ADR-0015). Surface réduite par le snapshot à `create` seul (upload AAB/APK multipart — ni read ni delete) ; création irréversible → gate `--confirm` + `CAN_CREATE_MANAGED_PLAY_APPS` + enrollment managed Play.

### Games Services — `gamesConfiguration` — ✅ **livré** (PRD [#241](https://github.com/PollyGlot/google-play-cli/issues/241), slices [#286](https://github.com/PollyGlot/google-play-cli/issues/286)–[#288](https://github.com/PollyGlot/google-play-cli/issues/288))
Configuration des achievements et leaderboards Play Games Services (CRUD).
**Statut :** livré `[experimental]` — `games achievements list/view/create/update/delete` et `games leaderboards list/view/create/update/delete` ([ADR-0033](adr/0033-games-services-configuration-draft-crud.md)) ; API séparée (Play Games Services *Publishing* API — la partie runtime reste exclue par nature), namespace `games` (`achievements`/`leaderboards`), addressing par **Play Games application ID** (`--application-id`), scope `androidpublisher` partagé. Le snapshot a corrigé le draft : **pas d'`imageConfigurations`** (upload d'icônes inexistant) et **pas de méthode publish** (l'API écrit le *draft* ; `published` read-only ; publication joueurs Console-only). `delete` est gated `--confirm` (exit 3) ; `create`/`update` acceptent les flags localisés ou `--from-json` (round-trip ADR-0003). **Reporté :** le *sweep* de localisation en masse (ajouter une locale sur tous les achievements d'un coup) — workflow d'ordre supérieur.

### Play Integrity API — ❌ **non-goal par nature ([ADR-0026](adr/0026-maximal-admin-api-coverage.md))**
Vérification serveur, par requête, qu'un appel au backend vient d'un binaire et d'un device authentiques.
**Statut :** exclu définitivement — API *runtime* (token éphémère généré on-device, consommé côté serveur) : structurellement inutilisable depuis un terminal ou un agent. La seule API développeur Play que gplay n'enveloppera jamais.

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

### Soumission à homebrew-core (nom nu `gplay` au lieu de `pollyglot/tap/gplay`)
Faire graduer la formule du tap tiers `pollyglot/tap` vers `Homebrew/homebrew-core`, ce qui ferait afficher `gplay` en nom court (comme `asc`) au lieu du chemin `owner/repo/formule` — purement cosmétique, `brew install gplay` marche déjà en nom court via le tap.
**Pourquoi parké :** bloqué sur le gate de notoriété de `brew audit --new` (`SharedAudits`). Rejet « not notable enough » tant que forks < seuil ET watchers < seuil ET stars < seuil. Comme **on est owner du repo**, c'est le seuil **self-submission ×3** qui s'applique : < 90 forks, < 90 watchers, < 225 stars. État actuel (2026-07-20) : 5 stars / 0 fork / 1 watcher → très loin. L'âge (~63 j > 30 j requis), la licence (MIT/DFSG) et le tag stable sont OK.
**Voie la plus atteignable :** faire soumettre par un **contributeur tiers** (seuil standard, non ×3) → il suffit alors de **75 stars OU 30 forks OU 30 watchers** (un seul suffit). Cible réaliste : ≥ 75 stars. Coûts à peser : réécriture de la formule en **build-from-source** (homebrew-core refuse les binaires prébuilds goreleaser) + perte du contrôle du timing des releases (chaque bump = PR relue + BrewTestBot au lieu du push direct au tap).
**Ne pas ouvrir ce chantier tant qu'on est loin de ~75 stars.**

---

## Documentation & onboarding

### Tableau de migration Fastlane → gplay complet
Mapping détaillé `fastlane supply` ↔ `gplay`, options par options, avec exemples côte à côte.
**Pourquoi plus tard :** à écrire une fois qu'on a du retour de vrais migrateurs sur les pièges réels. Écrit en aveugle, le tableau sera incomplet.

### Exemples CI/CD multi-providers
`docs/CI_CD.md` ne couvre que GitHub Actions en MVP. Bitrise, GitLab CI, CircleCI, Jenkins à venir.
**Pourquoi plus tard :** patterns identiques modulo l'injection de secrets. Ajouter les exemples concrets quand un user d'un provider donné en a besoin.

### Lentille « commande gplay » pour `gplay schema` — sous-feature de [#199](https://github.com/PollyGlot/google-play-cli/issues/199)
`gplay schema` ([#199](https://github.com/PollyGlot/google-play-cli/issues/199)) indexe la surface API par **method id natif** ([ADR-0022](adr/0022-schema-index-keyed-by-api-method.md)). Une couche complémentaire indexerait plutôt par **commande gplay** (`tracks set`, `metadata apply`) pour répondre « quels champs puis-je envoyer à `tracks set` ».
**Pourquoi plus tard :** non dérivable du Discovery snapshot — le mapping commande→appels API vit dans le code de gplay et est lossy/N:1 (`metadata apply` = N `listings.patch` ; `apps view` = `details.get` + `listings.get`). C'est une feature séparée qui se pose *au-dessus* de l'index keyé par method id, pas un remplacement. Le besoin immédiat est déjà couvert par `gplay <cmd> --help` + les skills.
