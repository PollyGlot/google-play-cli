# Couverture API — carte « ce qu'on a vs. tout le reste »

> **But de ce doc :** visualiser d'un coup d'œil la surface de la **Google
> Play Developer API** (+ APIs liées) face à ce que `gplay` couvre
> aujourd'hui, pour piloter la priorisation v1 / post-v1. C'est une **photo
> de travail** (générée le 2026-06-01), pas un contrat — la source de vérité
> reste [ROADMAP.md](ROADMAP.md), [BACKLOG.md](BACKLOG.md) et les issues PRD.

## Légende

| Statut | Sens |
|---|---|
| ✅ **Livré** | Implémenté et mergé dans `main` |
| 📋 **Planifié** | PRD ouvert, déjà cadré (Route 1.x) |
| 🅿️ **Backlog** | Reporté explicitement (rationnel dans BACKLOG.md) |
| 🚫 **Non-goal** | Probablement hors périmètre d'un CLI d'admin |
| ➖ **Non tracké** | Existe dans l'API, jamais évalué chez nous |

## Tableau de bord

| | Familles API | Détail |
|---|---|---|
| ✅ Livré | **5** surfaces | auth · apps (registre local) · releases/tracks · reviews (7j) · metadata listings |
| 📋 Planifié | **8** PRD | *first-release :* images · details+country · data safety · tracks/testers — *ensuite :* vitals · monetization · reviews-history · apk |
| 🅿️ Backlog | **~7** surfaces | obb/apprecovery · purchases · internal-sharing · custom apps · auto-discovery… |
| 🚫 / ➖ | **~8** surfaces | integrity · PAD · users/grants · deviceTierConfigs · generatedapks · externaltransactions… |

---

## Carte visuelle (par famille, colorée par statut)

```mermaid
flowchart TB
    classDef done    fill:#1a7f37,color:#fff,stroke:#0b5,stroke-width:1px;
    classDef planned fill:#1f6feb,color:#fff,stroke:#1158c7,stroke-width:1px;
    classDef parked  fill:#57606a,color:#fff,stroke:#424a53,stroke-width:1px;
    classDef nongoal fill:#8b1a1a,color:#fff,stroke:#6e1313,stroke-width:1px;

    subgraph PUB["📦 Publication — Edits API (edits.*)"]
        direction TB
        E_life["Edits lifecycle\ninsert/commit/validate"]:::done
        E_bund["bundles (AAB upload)"]:::done
        E_track["tracks (releases/rollout)"]:::done
        E_list["listings (textes/locale)"]:::done
        E_img["images (screenshots, icône)\nPRD #112"]:::planned
        E_det["details (langue, contact)\nPRD #113"]:::planned
        E_ctry["countryavailability\nPRD #113"]:::planned
        E_deob["deobfuscationfiles\n(mappings ProGuard) → #49"]:::planned
        E_apk["apks (APK legacy)\nplus tard → #118"]:::planned
        E_obb["expansionfiles (OBB)"]:::parked
        E_test["testers + custom tracks\n(test fermé 1ère release)"]:::planned
        E_rec["apprecovery (rollback ciblé)"]:::parked
    end

    subgraph REV["⭐ Reviews (reviews.*)"]
        direction TB
        R_list["list / get / reply (7 jours)"]:::done
        R_hist["historique >7j (CSV GCS)\nPRD #94"]:::planned
    end

    subgraph MON["💰 Monetization"]
        direction TB
        M_sub["subscriptions v2\n(+ basePlans + offers) → #51"]:::planned
        M_iap["inappproducts (IAP one-shot)\n→ #51"]:::planned
        M_rc["sync RevenueCat → #51"]:::planned
        M_pur["purchases.* (validation tokens)"]:::parked
        M_ext["externaltransactions"]:::nongoal
    end

    subgraph VIT["📊 Vitals — Reporting API (service séparé)"]
        direction TB
        V_crash["crashrate / anrrate\nerrors.reports/counts → #49"]:::planned
        V_perf["slowstart / slowrendering\nwakeups / wakelocks → #49"]:::planned
        V_anom["anomalies"]:::nongoal
    end

    subgraph CONF["⚙️ Config & conformité app"]
        direction TB
        C_data["applications.dataSafety\nPRD #114"]:::planned
        C_tier["deviceTierConfigs"]:::nongoal
        C_gen["generatedapks (download splits)"]:::nongoal
        C_sys["systemapks"]:::nongoal
    end

    subgraph ACCT["👥 Compte & partage"]
        direction TB
        A_share["internalappsharingartifacts"]:::parked
        A_cust["customapps (Managed Play)"]:::parked
        A_users["users / grants (permissions)"]:::nongoal
    end

    subgraph LOCAL["🔑 gplay-natif (hors API REST)"]
        direction TB
        L_auth["auth (SA→OAuth2, keystore)"]:::done
        L_apps["apps registre local\ninit/add/list/info/remove"]:::done
        L_out["output TTY-aware + markdown"]:::done
        L_disc["auto-discovery apps\n(Cloud Resource Manager/IAM)"]:::parked
    end

    subgraph ADJ["🧩 APIs adjacentes"]
        direction TB
        J_integ["Play Integrity API"]:::nongoal
        J_pad["Play Asset Delivery"]:::parked
    end
```

> Astuce : ouvre ce fichier dans un viewer Markdown qui rend Mermaid (GitHub,
> aperçu VS Code) pour la version colorée. **Vert = fait · Bleu = planifié ·
> Gris = backlog · Rouge sombre = non-goal/non tracké.**

---

## Détail par famille

### 📦 Publication — Edits API (cœur « remplacer Fastlane »)

| Surface API | Ce que ça permet | Statut | Réf |
|---|---|---|---|
| Edits lifecycle | Le modèle transactionnel insert→change→commit | ✅ | mode implicit ; explicit parké [#48](https://github.com/PollyGlot/google-play-cli/issues/48) |
| `edits.bundles` | Upload AAB | ✅ | PRD #4 |
| `edits.tracks` | Releases, promote, rollout, halt/resume/complete | ✅ | PRD #4/#5 |
| `edits.listings` | Titre, descriptions, vidéo YouTube par locale | ✅ | PRD [#50](https://github.com/PollyGlot/google-play-cli/issues/50) |
| `edits.images` | Screenshots, icône, feature graphic par locale/format | 📋 | PRD [#112](https://github.com/PollyGlot/google-play-cli/issues/112) |
| `edits.details` | Langue par défaut, email/tel/site contact | 📋 | PRD [#113](https://github.com/PollyGlot/google-play-cli/issues/113) |
| `edits.countryavailability` | Pays de distribution | 📋 | PRD [#113](https://github.com/PollyGlot/google-play-cli/issues/113) |
| `edits.deobfuscationfiles` | Upload `mapping.txt` (dé-obfuscation crashes) | 📋 | couplé vitals [#49](https://github.com/PollyGlot/google-play-cli/issues/49) |
| `edits.apks` | Upload APK legacy | 📋 | PRD [#118](https://github.com/PollyGlot/google-play-cli/issues/118) — apps existantes only, **plus tard** (AAB obligatoire pour les nouvelles apps depuis 2021) |
| `edits.expansionfiles` | OBB / fichiers d'expansion | 🅿️ | legacy, remplacé par PAD |
| `edits.testers` + `tracks.create` | Testeurs + création de custom closed tracks (test fermé obligatoire avant 1ère prod) | 📋 | thème first-release — PRD [#117](https://github.com/PollyGlot/google-play-cli/issues/117) |
| `edits.apprecovery` | Rollback ciblé pour users impactés | 🅿️ | incident response, niche |

### ⭐ Reviews

| Surface API | Ce que ça permet | Statut | Réf |
|---|---|---|---|
| `reviews.list/get/reply` | Lire et répondre (fenêtre 7 jours) | ✅ | PRD #6 |
| CSV reports GCS | Historique complet >7j (autre scope OAuth) | 📋 | PRD [#94](https://github.com/PollyGlot/google-play-cli/issues/94) — spike d'abord |

### 💰 Monetization

| Surface API | Ce que ça permet | Statut | Réf |
|---|---|---|---|
| `monetization.subscriptions` (+basePlans/offers) | Abonnements v2 (3 niveaux imbriqués) | 📋 | PRD [#51](https://github.com/PollyGlot/google-play-cli/issues/51) |
| `inappproducts` | CRUD produits IAP one-shot | 📋 | PRD [#51](https://github.com/PollyGlot/google-play-cli/issues/51) |
| Sync RevenueCat | Réconcilier catalogue ASC/Play ↔ RevenueCat | 📋 | PRD [#51](https://github.com/PollyGlot/google-play-cli/issues/51) |
| `purchases.*` | Validation serveur des tokens d'achat | 🅿️ | API serveur, pas un usage CLI naturel |
| `externaltransactions` | Reporting external offers program | ➖ | jamais évalué |
| `monetization.onetimeproducts` | Nouvelle API one-time (remplace `inappproducts`) | ➖ | à arbitrer dans #51 (legacy vs. nouvelle API) |

### 📊 Vitals — Play Developer **Reporting** API (`androidvitals`, service séparé)

| Surface API | Ce que ça permet | Statut | Réf |
|---|---|---|---|
| `vitals.crashrate` / `anrrate` / `errors.*` | Taux crashes/ANR, rapports & counts d'erreurs | 📋 | PRD [#49](https://github.com/PollyGlot/google-play-cli/issues/49) |
| `vitals.slowstart` / `slowrendering` / wakeups / wakelocks | Métriques perf secondaires | 📋 | même module #49 |
| `anomalies` | Détection d'anomalies sur les métriques | ➖ | jamais évalué |

### ⚙️ Config & conformité

| Surface API | Ce que ça permet | Statut | Réf |
|---|---|---|---|
| `applications.dataSafety` | Déclaration Data Safety form | 📋 | PRD [#114](https://github.com/PollyGlot/google-play-cli/issues/114) |
| `applications.deviceTierConfigs` | Tiers d'appareils (Play Asset Delivery) | ➖ | jamais évalué |
| `generatedapks` | Télécharger les APK splits générés depuis un AAB | ➖ | jamais évalué |
| `systemapks` | Variantes APK système (préinstall OEM) | ➖ | très niche |

### 👥 Compte & partage

| Surface API | Ce que ça permet | Statut | Réf |
|---|---|---|---|
| `internalappsharingartifacts` | Upload pour partage par lien (hors track) | 🅿️ | workflow QA spécifique |
| `customapps` | Distribution privée (Managed Google Play) | 🅿️ | niche entreprise |
| `users` / `grants` | Gérer utilisateurs & permissions Play Console | ➖ | admin de compte, jamais évalué |

### 🔑 gplay-natif (pas de l'API REST mais essentiel)

| Brique | Statut | Réf |
|---|---|---|
| auth (service account → OAuth2, keystore) | ✅ | PRD #2 |
| apps — **registre local** (pas d'`apps.list` dans l'API) | ✅ | PRD #3 |
| output TTY-aware + markdown | ✅ | PRD #26 |
| auto-discovery apps via Cloud Resource Manager/IAM | 🅿️ | casse la promesse « juste le SA JSON » |

### 🧩 APIs adjacentes

| Surface | Statut | Pourquoi |
|---|---|---|
| Play Integrity API | 🚫 | consommée par un backend runtime, pas un outil d'admin |
| Play Asset Delivery (PAD) | 🅿️ | config Gradle, peu de surface CLI |

---

## Synthèse pour la priorisation

**Décision (2026-06-01) — on reste en `0.x` rolling.** Pas de gel 1.0 planifié à
court terme ; on shippe large (plusieurs features par mineure). La politique
[ADR-0010](adr/0010-versioning-public-contract-and-ga.md) reste valable mais **en
sommeil**. Voir [ROADMAP.md](ROADMAP.md) pour le séquençage à jour.

**North star near-term : « zéro → 1ère release prod ».** gplay doit préparer et
publier une app de bout en bout. Parcours mappé :

| Étape | Surface API | Statut |
|---|---|---|
| Auth + enregistrer l'app | auth · apps | ✅ |
| Textes du store | `edits.listings` | ✅ |
| Visuels (screenshots, icône, feature graphic) | `edits.images` | 📋 [#112](https://github.com/PollyGlot/google-play-cli/issues/112) |
| App info (langue défaut, contacts, catégorie) | `edits.details` | 📋 [#113](https://github.com/PollyGlot/google-play-cli/issues/113) |
| Pays de distribution | `edits.countryavailability` | 📋 [#113](https://github.com/PollyGlot/google-play-cli/issues/113) |
| Data safety (obligatoire) | `applications.dataSafety` | 📋 [#114](https://github.com/PollyGlot/google-play-cli/issues/114) |
| Test fermé obligatoire (12 testeurs / 14j) | `tracks.create` + `edits.testers` | 📋 [#117](https://github.com/PollyGlot/google-play-cli/issues/117) |
| Upload AAB + track + rollout | `bundles` · `tracks` | ✅ |
| ⚠️ Content rating, public cible, déclarations | — | ❌ pas d'API → manuel console |

**Ensuite (post first-release, toujours `0.x`) :** vitals ([#49](https://github.com/PollyGlot/google-play-cli/issues/49),
observabilité post-lancement) → monetization ([#51](https://github.com/PollyGlot/google-play-cli/issues/51),
**post-v1**) → reviews-history ([#94](https://github.com/PollyGlot/google-play-cli/issues/94),
spike d'abord). APK upload (`edits.apks`) = PRD séparé pour apps **existantes**,
hors thème first-release.

---

*Voir aussi : [ROADMAP.md](ROADMAP.md) · [BACKLOG.md](BACKLOG.md) ·
[CONTEXT.md](../CONTEXT.md) · [ADR-0010](adr/0010-versioning-public-contract-and-ga.md)*
