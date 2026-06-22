# Context — gplay

Glossary of canonical terms for this project. Implementation details belong elsewhere (code, ADRs, README). This file is a glossary and nothing else.

## Terms

### gplay
The binary name of this CLI. Short, easy to type in CI scripts. Distinct from the existing TypeScript alternative `gpc` by yasserstudio and from `gpc` (GNU Pascal Compiler).

### Edit
A transactional unit on the Google Play Developer API. An edit is opened on a specific package, accumulates changes (metadata, listings, releases, tracks), and is committed atomically. Edits expire after ~24h and are exclusive per app (only one open at a time).

The CLI exposes edits in two modes:

- **Implicit** — most write commands (`gplay metadata apply`, `gplay releases upload`, etc.) open, modify, and commit their own edit in a single invocation. This is the default for the 90% case.
- **Explicit** — `gplay edits begin / commit / discard` lets the caller batch multiple changes into one transaction. While an explicit edit is open, its ID is persisted to `.gplay/edit-<package>.json` in the working directory. Subsequent write commands detect that file and reuse the open edit instead of creating their own.

We keep the term **edit** (Google's official vocabulary) rather than renaming to "transaction" or "changeset" — this matches the API docs and reduces friction for users reading both.

### Account
A named credential profile registered in gplay. One Account = one Google Cloud service account JSON. Multiple Accounts can coexist (different apps, different orgs, dev vs CI). Exactly one Account is **active** at a time and is used when no `--account` flag is passed. The active Account is recorded in the gplay config file; the credential itself lives in the OS keystore (or a 0600 fallback file).

Distinct from Google's "service account" (the GCP-side IAM principal): an `Account` in gplay is the **local registration** of a service account, with a human-friendly name and the active flag.

### Project
A repo-local pinning of a gplay invocation to a specific Android package. Created by `gplay init --package com.example.myapp`, which writes `.gplay/config.json` at the repo root. Any subsequent gplay command run inside that tree (walking up to find `.gplay/`) defaults its target to that package, so `--package` becomes optional.

A Project pins a **package only** — not an Account. Account resolution stays separate (config-wide active or env/flag).

Coexists in `.gplay/` with `edit-<package>.json` (open explicit Edit, see above). `.gplay/` is meant to be committed for `config.json` and gitignored for `edit-*.json` (transient).

### Listing
The textual store-front of an app **for one locale**: `title`, `shortDescription`, `fullDescription`, and an optional promo `video` URL. Backed by Google's `edits.listings` resource. A locale that has any of these is said to "have a Listing"; `deletegroup` removes a locale's Listing entirely.

On disk, each field maps to a fastlane-named file inside the locale directory: `title.txt`, `short_description.txt`, `full_description.txt`, `video.txt` (snake_case, identical to `fastlane supply` so an existing tree is a drop-in). Google's limits — title 30, short 80, full 4000 chars — are enforced offline by `gplay metadata validate`.

Deliberately **excludes** release notes (the per-release "what's new" text): those are not a Listing field on Google Play and gplay owns them under `releases upload --release-notes[-dir]`, not under metadata. It also excludes app-level details (contact email, default language — `edits.details`), a separate app-global API resource owned by the `apps` namespace, and store images (`edits.images`), which are a separate API resource owned by the sibling `metadata images` sub-namespace (see Store image) — never a Listing field.

The canonical on-disk form of a set of Listings is the **metadata tree** (see below).

### Store front
The **per-locale** presentation of an app on Google Play: its Listings (text) and its store images (icon, feature graphic, screenshots per locale and form factor — see Store image). This is the axis the `metadata` command namespace owns, with text under the Listing commands and images under the `metadata images` sub-namespace. Deliberately distinct from **app-global** configuration — default language and contact details (`edits.details`) — which is keyed by app, not by locale, and belongs with the `apps` namespace (gplay already reads details via `apps view`), never inside the metadata tree.

The dividing question for any new store-presence surface is its **key axis**: keyed by locale → store front → `metadata`; keyed by app → **App details** → `apps`; keyed by track → **Country availability** → `tracks`.

### App details
The app-global configuration block backed by Google's `edits.details` resource: `defaultLanguage` plus the user-visible contact `email`, `phone`, and `website`. Keyed by **app** — one set per package, independent of locale (unlike a Listing) and of track (unlike Country availability). The only writable app-global surface gplay exposes today; read with `apps details view`, written field-by-field with `apps details set`.

Distinct from **apps view**, the cross-resource *identity card* (package + title + default language, where `title` comes from a Listing, not from details). App details is the full `edits.details` record; `apps view` is a deliberately terse "am I looking at the right app?" check.

_Avoid_: calling these fields "metadata" — metadata is the per-locale Store front.

### Store image
A binary store asset attached to a Listing, keyed by **locale and image type** (`edits.images`): the singular slots `icon`, `featureGraphic`, `tvBanner`, `promoGraphic`, and the gallery slots `phoneScreenshots`, `sevenInchScreenshots`, `tenInchScreenshots`, `tvScreenshots`, `wearScreenshots`. Part of the Store front (per-locale), so it lives under `metadata`, in a `metadata images` sub-namespace distinct from the text Listing commands — the two share the metadata tree and the Additive sync stance but reconcile differently (text upserts by field name; images diff by content, see Image slot).

Unlike a Listing field, a Store image has **no caller-assigned key**: the API identifies each image by a server-assigned `id` and a content `sha256`, and `edits.images.upload` cannot name a slot position. Reconciliation is therefore by content hash, not by field name.

### Image slot
The unit `metadata images apply` reconciles: one `(locale, image type)` pair. A **singular** slot (`icon`, `featureGraphic`, `tvBanner`, `promoGraphic`) holds at most one image; a **gallery** slot (`phoneScreenshots`, `sevenInchScreenshots`, `tenInchScreenshots`, `tvScreenshots`, `wearScreenshots`) holds an **ordered sequence** where the **on-disk filename sort is the display order** (the `edits.images` API carries no order field). Reconciliation is by content hash with `missing == empty`, which is why images are a separate sub-namespace rather than folded into `metadata apply` — see [ADR-0013](./docs/adr/0013-image-slot-reconciliation.md).

### Metadata tree
The on-disk form of an app's Store front: a directory (`--dir`, default `./metadata`) with one sub-directory per locale, each holding one `.txt` file per Listing field plus an optional `images/` sub-directory for its Store images. It mirrors the shape `fastlane supply` reads — minus fastlane's redundant `android/` segment and its `changelogs/` (release notes live with `releases`, not metadata). It is the unit `gplay metadata pull` writes and `gplay metadata apply` reads.

Within a locale's directory, a **missing** field file and an **empty** field file mean different things to `apply`: missing = "I don't manage this field, leave the online value alone"; empty = "clear this field online". `pull` preserves this by writing a file only for a non-empty online field, so `pull` then `apply` with no edits is a no-op.

### Additive sync
gplay's reconciliation stance for the Metadata tree: `apply` only ever upserts the locales and fields it finds on disk; anything live on Play but absent locally is left untouched, never deleted by omission. Deletion is opt-in via `--prune` (which also refuses to remove the app's `defaultLanguage` Listing). The mirror stance (disk is the sole source of truth, online-only locales get deleted) is deliberately **not** the default — it makes a partial `pull` followed by `apply` a data-loss event.

### Standard track
One of the four tracks Google Play provisions for **every** app, in promotion order: `internal`, `alpha`, `beta`, `production`. gplay derives the standard-vs-closed **kind** from the track name alone — the API carries no such field — so the distinction lives only in the human (`table`/`markdown`) views, never the JSON pass-through.

### Closed track
A testing track an app owner creates **beyond** the four Standard tracks, with a custom name (`qa-team`, `external-beta`, …), to gate a build to an explicit audience. It is the **only** kind of track gplay can create: the create endpoint's sole supported type is closed testing (no API path creates an open or internal track). Carries a form factor (phone by default).

_Avoid_: "custom track" on its own as the canonical noun — prefer **Closed track**.

**Flagged ambiguity**: `tracks list` renders a derived `kind` column whose value for these is `custom`, not `closed`. They denote the same thing — every non-Standard track is a Closed track, since closed testing is the only creatable type. `custom` is a display label for the kind axis (standard ↔ custom); **Closed track** is the canonical concept.

### Tester
The unit of test audience gplay manages on a track. On Google Play's API the authorized audience is expressed **only** as Google Groups (an array of group email addresses, e.g. `qa@googlegroups.com`), never as individual tester emails — adding people one-by-one is a Play Console-only gesture and is **out of gplay's scope**. A Tester set is keyed by track (one per track) and managed declaratively: `gplay testers set` replaces the whole list of groups, `gplay testers list` reads it.

_Avoid_: treating a "tester" as an individual person or a bare email address — in gplay the addressable tester unit is a **Google Group**.

### Country availability
The set of countries an app's artifacts are distributed to **on a given track**, backed by Google's `edits.countryavailability` resource: `syncWithProduction`, `restOfWorld`, and the list of targeted `countries[]` (CLDR two-letter codes). Keyed by **track**, not by app — there is no app-global "where is this app available" in this resource; you ask it per track.

**Read-only on the Developer API** (the resource exposes only `get`; setting availability is a Play Console gesture). gplay therefore surfaces it as a read under the `tracks` namespace, never an editable `apps` field.

_Avoid_: describing Country availability as "app-global" or filing it under `apps` — both contradict the track-keyed, read-only reality.

### Compliance
The command family for an app's **regulatory declarations** — the Play Console gestures that **gate publication** rather than shape store presence: Data Safety today, content rating and other "App content" declarations in future scope. A Compliance surface is recognised by a cluster of traits that no `metadata`/`apps`/`tracks` surface shares: keyed by **app**, **write-heavy or write-only**, **outside the Edits model**, and a **publication gate** (Google blocks releases if it is stale). This is why Compliance is its own namespace rather than folding app-keyed declarations into `apps`: the store-presence axis test (locale→`metadata`, app→`apps`, track→`tracks`) is scoped to *store presence* and does not bind a regulatory surface.

_Avoid_: filing a regulatory declaration under `metadata` (it is not the per-locale Store front) or under `apps` (that namespace is identity + editable app-global config, not regulatory gates).

### Data Safety declaration
The app-global statement of what user data an app collects, how it is shared with third parties, and its security practices — backed by Google's `applications.dataSafety` resource and surfaced by gplay under `compliance datasafety`. Keyed by **app**, it is a **publication gate**: Google blocks releases when it is out of date, so it bears on **every** app, not only monetised ones.

gplay can **set it but never read it back**: the Developer API exposes only a write (`POST …/applications/{package}/dataSafety`) — there is no `get`. This single fact, not a preference, is why there is no `compliance datasafety pull`, why the [Additive sync](#additive-sync) model of [ADR-0011](docs/adr/0011-metadata-apply-sync-model.md) does **not** apply (no diff is possible), and why a dry-run can only be offline. The declaration travels as **one CSV document** — the same import/export CSV the Play Console uses, adopted verbatim (gplay does not re-model Google's evolving schema), versioned in the repo as a plain file.

_Avoid_: calling it "metadata" (it is a compliance gate, not Store front) or implying gplay can show the live declaration (it cannot — the API is write-only).

### Format
A way of shaping a command's output for a specific reader: `table` for humans on a TTY, `json` for machines and scripts, `markdown` for documentation, chat agents, and PR comments. Selected by `--output`. The special value `auto` (the default, encoded as the empty string on the flag) lets the CLI pick based on context: `json` when stdout is not a TTY or when `CI=true`, `table` otherwise.

`json` is API pass-through (see [ADR-0003](./docs/adr/0003-json-passthrough.md)). `table` and `markdown` are human-shaped views; each command chooses how to render its data in those formats.

### Renderer
A function that turns a command's data payload into bytes for a given Format. Each command supplies its own three Renderers (one per Format), and a shared dispatcher in `internal/output` picks the right one based on the resolved Format. The dispatcher owns the "unsupported format" error path so each command stops re-implementing it.

### Release
Cutting a new published version of the **gplay CLI** itself — the versioned binaries, archives, checksums, signature and GitHub Release, tagged `vX.Y.Z`. Concerns the CLI distribution only: the installer needs no redeploy (`install.sh` resolves the latest release itself). Distinct from **Deploy** (the gplay.sh site), though a published Release also triggers a Deploy to refresh the generated CLI reference.

### Deploy
Publishing a new version of the **gplay.sh Worker** — the single Cloudflare Worker serving both the website and the install script behind `https://gplay.sh/install`. Distinct from **Release**: a Deploy concerns the gplay.sh site + install proxy, a Release concerns the CLI artifacts. Trigger logic (path-filtered pushes, published releases): [ADR-0025](./docs/adr/0025-website-served-from-install-worker.md).

### Public contract
The subset of gplay's interface that a Release promises not to break without a major version bump: command and flag names and their semantics, exit codes, the config schema and resolution precedence, Account resolution precedence, and the guarantee that `--output json` stays API-passthrough **for commands that wrap a Developer API call**. Offline reference commands (`team permissions`, `schema`) wrap no API call and **synthesise their own JSON** — a gplay-owned, per-command shape, not an API echo (and an `[experimental]` command's shape is explicitly outside the contract until it graduates). Deliberately **excluded**: the `table` / `markdown` layouts (human views, free to evolve), the *fields* inside the passthrough JSON (owned by the Google Play Developer API, not gplay), and stderr / log wording. Distinct from "everything the CLI does" — the Public contract is only the part under an explicit stability promise. See [ADR-0010](./docs/adr/0010-versioning-public-contract-and-ga.md) and [DESIGN §7](./docs/DESIGN.md).

### Stability label
A per-command lifecycle marker shown in help output, signalling how far a command's surface can be depended on: **no label** = part of the Public contract (stable); **`[experimental]`** = shipped but still evolving, outside the contract; **`DEPRECATED:`** = a compatibility path kept during a migration, not the long-term home. Lets a command ship before it is frozen.

### Public preview / GA
Two named maturity states of the gplay CLI. **Public preview** is the `v0.x` line: publicly installable and usable, an invitation to test and give feedback, with breaking changes still possible — no Public contract promise yet. **GA** (general availability, `v1.0`+) is the state where the Public contract is in force. These are communication-and-promise states, not feature-count thresholds.

### Developer account
The Play Console organisation that owns apps, keyed by a numeric `developerId`. It is the unit `gplay team` manages: its members (Users) and their permissions. Distinct from **Account** (a gplay credential profile) and from **service account** (the GCP IAM principal a credential authenticates as). A service account is linked to a Developer account on the Play Console — typically one, though an account can in principle invite the same service-account email into several — and a gplay **Account** is the local registration of that service account. It is the first **account-scoped** addressing axis gplay carries: keyed by the org, not by a package or a locale.

_Avoid_: writing "account" unqualified anywhere near `team` — three different things end in "account" here. **Developer account** = the Play Console org; **Account** = the local gplay credential profile; **service account** = the GCP IAM principal.

### User
A person who is a member of a **Developer account**, identified by email, carrying account-wide `developerAccountPermissions` and optional per-app **Grants**. Managed under `gplay team users`. Explicitly distinct from **Account**: a User is a Play Console teammate, not a credential. The same email may also be a service account's, when a service-account principal is itself invited as a member.

_Avoid_: calling a User an "account" (it is a person/principal *inside* a Developer account), or conflating it with the gplay **Account** that authenticates the call.

### Grant
A **User**'s access to a single app, carrying `appLevelPermissions` (e.g. reply-to-reviews only on `com.example.foo`). Keyed by (User, package). It is a **field of the User resource** — there is no standalone grants-list endpoint, so a User's grants are read back from the User — and is managed under `gplay team grants`.

_Avoid_: confusing a Grant (one person's per-app access) with a **Tester** (a per-track test audience of Google Groups) — both are "access to an app" but on unrelated axes.

### Permission
A single capability a **User** or **Grant** can hold, expressed in the API as a `CAN_*` enum — account-wide (`developerAccountPermissions`, the `_GLOBAL`-suffixed family) on a User, or per-app (`appLevelPermissions`) on a Grant. The two families are near-parallel: most app-level values are the account-level one minus the `_GLOBAL` suffix; the account family adds inherently account-wide capabilities (Play Games, managed Play, connected apps). gplay never invents a Permission — it only ever sends values Google defines, and rejects the non-grantable `*_UNSPECIFIED` sentinels.

### Permission alias
A gplay-friendly, **scope-independent** name for a **Permission** — e.g. `release-production` for `CAN_MANAGE_PUBLIC_APKS` (under `team grants`) or `CAN_MANAGE_PUBLIC_APKS_GLOBAL` (under `team users`). The same alias resolves to the app-level or the account-level enum depending on the command's scope. Aliases are curated for the meaningful, non-deprecated permissions; the raw `CAN_*` enum is **always also accepted**, so a permission gplay has no alias for is never un-grantable. Lets a caller — especially an agent — express intent without memorising Google's vocabulary.

### Role bundle
A gplay-defined, **frozen** preset that expands to a fixed set of Permissions — `viewer`, `reviewer`, `tester-manager`, `release-manager`, `admin` — selected with `--role`. A convenience over enumerating aliases one by one. *Frozen* means a bundle's membership never changes silently as Google adds permission enums: a new enum joins a bundle only by an explicit, versioned gplay change. Deliberately **excludes** sensitive money capabilities (financial data, orders): those are expressible only as explicit Permissions, never hidden inside a role. Distinct from a **Permission alias** (one Permission) — a Role bundle is a *set*.

### Admin API
A Google API a developer (or their agent/CI) calls **about** their app — configuration, publication, distribution, reporting, team. The scope test of [ADR-0026](docs/adr/0026-maximal-admin-api-coverage.md): every Play admin API is in gplay's scope in full. Opposed to a **runtime API**, one the app or its backend calls while **serving end users** (per-request, often with device-generated ephemeral tokens) — structurally unusable from a terminal and therefore out of scope by nature, not by priority.

_Avoid_: arguing a surface out of scope because it is "niche" or "nobody will find it" — under ADR-0026 the only exclusion test is admin vs runtime.

### Android Publisher API
The core Google Play Developer API (`androidpublisher`, v3) — the service gplay has wrapped from day one. Owns publication (the Edit model: listings, images, details, tracks, releases, testers), monetization (in-app products, subscriptions), distribution extras (internal app sharing, device tier configs, app recovery), compliance (Data Safety) and reviews. ~137 methods. One of several Play admin APIs — not a synonym for "the Play API".

### Play Developer Reporting API
The separate Google service (`playdeveloperreporting`) for **post-launch observability**: crash and ANR rates, error reports and counts, slow start/rendering, wakeups/wakelocks, anomalies. Same service-account auth as the Android Publisher API but a distinct service with its own Discovery snapshot. The data source for the planned `vitals` namespace. Only useful with deobfuscation mappings uploaded (`edits.deobfuscationfiles`, an Android Publisher resource).

_Avoid_: conflating it with the Android Publisher API or assuming one Discovery snapshot covers both.

### Play Games Services Publishing API
The Google service (`gamesConfiguration`) for configuring a game's Play Games Services resources — achievement and leaderboard configurations (CRUD on each: list/view/create/update/delete). An Admin API (developer configures their game), distinct from the Play Games Services *runtime* APIs the game itself calls (sign-in, score submission), which are out of scope. Addressed by the **Play Games application ID** (a numeric ID in its own space, not the Android package). The API writes the editable **draft**; the `published` copy is read-only and there is **no publish method** — publishing to players is Console-only ([ADR-0033](docs/adr/0033-games-services-configuration-draft-crud.md)). gplay plans the `games` namespace (`games achievements` / `games leaderboards`).

_Avoid_: assuming an icon/image-upload surface (`imageConfigurations` is not in the current API); modelling draft→published as an [Edit](#edit) (there is no commit/publish method); addressing Games resources by Android package name.

### Custom app
A private Android app distributed only to one organisation through **managed Google Play**, created via the Play Custom App Publishing API (`playcustomapp`) — the one API path that can *create* an app record (public apps can only be created in the Play Console). An Admin API surface, in scope under [ADR-0026](docs/adr/0026-maximal-admin-api-coverage.md), as the `customapps` namespace. Addressed on the **developer-account axis** (`accounts/{account}`, [ADR-0015](docs/adr/0015-developer-account-addressing-rides-on-account.md)), not the package axis. The API exposes only `create` (a multipart AAB/APK upload — no read, no delete); because creation is irreversible, gplay gates it behind `--confirm` and requires `CAN_CREATE_MANAGED_PLAY_APPS` plus managed-Play enrollment ([ADR-0032](docs/adr/0032-custom-apps-account-axis-gated-creation.md)).

### Play Integrity API
Google's **runtime** API for verifying, per request, that a call to the developer's backend comes from a genuine app binary on a genuine device. The canonical example of a nature-excluded surface ([ADR-0026](docs/adr/0026-maximal-admin-api-coverage.md)): the verification token is generated on-device and consumed server-side within minutes — no terminal or agent session can hold one, so a gplay wrapper would be structurally unusable.

_Avoid_: listing Play Integrity in any coverage plan or skill; it is the one Play developer API gplay deliberately never wraps.

### Discovery snapshot
An offline, version-pinned copy of a Google API's **Discovery document** — Google's own machine-readable description of an API's *shape*: its methods, their request/response schemas, parameters and enums. It is a schema/contract, **not** data and **not** a changelog. gplay keeps one per service (`androidpublisher` today; the separate `playdeveloperreporting` Reporting service is its own snapshot, never conflated) so an agent or maintainer can answer "does this method exist, what's its request shape" by querying a local file instead of hitting the network.

It is a **query-only reference** (`jq`/`grep`), never read whole nor loaded into agent context, and never compiled into the binary (gplay speaks the API by hand — [ADR-0007](./docs/adr/0007-raw-http-not-google-go-sdk.md)). A single snapshot is a point-in-time photo; the *history* of Google's changes emerges from the file's git diffs, not from keeping multiple versions.

_Avoid_: calling it an "SDK" or implying the binary depends on it (it does not — it is dev/agent tooling), or treating one service's snapshot as covering another (each Google service has its own).

### Schema index
The derived, normalized catalog of an API's method surface that the `gplay schema` command queries — built offline from a [Discovery snapshot](#discovery-snapshot) and **embedded in the binary** (`//go:embed`), so introspection needs no network and no credentials. Two sections: `methods`, keyed by Google's native RPC **method id** (`androidpublisher.edits.tracks.update`), each carrying its HTTP method, path, parameters, and the *names* of its request/response schemas; and `schemas`, a dictionary keyed by type name (`Track`, `Release`) whose properties — with enums and Google's verbatim descriptions — are expanded once and referenced by name from methods and from each other, so nested types resolve by pointer rather than by duplication.

Distinct from the **Discovery snapshot** it derives from: the snapshot is the raw, full, query-only reference file (`jq`/`grep`, never compiled in); the Schema index is a trimmed, embedded projection of it, shaped for a queryable command. Embedding the index does **not** contradict [ADR-0007](docs/adr/0007-raw-http-not-google-go-sdk.md): it is inert reference data exposed by one command, never a dependency gplay routes API calls through (gplay still hand-rolls every HTTP call). It is keyed by the API method, never by a gplay command — mapping `gplay <cmd>` to the API calls it makes is a deliberately separate, deferred concern ([ADR-0022](docs/adr/0022-schema-index-keyed-by-api-method.md)).

_Avoid_: conflating the Schema index (derived, embedded, command-facing) with the Discovery snapshot (raw, on-disk, agent/maintainer-facing); calling either an "SDK".

### Vitals
Google Play's **post-launch quality signals** for an app — crash rate, ANR rate, slow start, slow rendering, excessive wakeups, low-memory kills, stuck background wakelocks — plus error reports/issues/counts and anomalies. Backed by the [Play Developer Reporting API](#play-developer-reporting-api) (`playdeveloperreporting`, v1beta1), a service **distinct** from the Android Publisher API: its own OAuth scope (`…/auth/playdeveloperreporting`) and its own [Discovery snapshot](#discovery-snapshot). Surfaced under the `gplay vitals` namespace, which is **read-only** — the API only reads metrics; nothing under `vitals` mutates Play state. The namespace name follows the API's `vitals.*` resource group and the "Android vitals" label users know from the Play Console, not the service name ([ADR-0027](docs/adr/0027-vitals-second-service-scope-readonly.md)).

_Avoid_: calling the service "androidvitals" (no such host — it is `playdeveloperreporting`); folding the [Mapping](#mapping) upload (a publisher Edit resource) under `vitals`.

### Metric set
The unit the [Play Developer Reporting API](#play-developer-reporting-api) exposes per vital: a named bundle of **metrics** (e.g. `crashRate`, `distinctUsers`) queryable across **dimensions** (e.g. `versionCode`, `deviceModel`, `countryCode`) over a **timeline** (aggregation `DAILY`/`HOURLY` in the metric set's fixed timezone), bounded by a **freshness** the API reports (the latest date carrying data). Each metric set offers a `get` (describe its supported metrics/dimensions/freshness) and a `query` (POST returning a timeline of rows). gplay never invents a metric or dimension — the supported set is read from the [Discovery snapshot](#discovery-snapshot)/[Schema index](#schema-index). `gplay vitals query <metric-set>` wraps the query directly; the opinionated `vitals crashes` / `vitals anr` / … commands are curated presets over it.

### Mapping
A ProGuard/R8 **deobfuscation file** (`mapping.txt`) uploaded so Play can symbolicate an app's obfuscated crash stack traces — without it, [Vitals](#vitals) error reports are unreadable. Backed by Google's `edits.deobfuscationfiles` resource: an **Android Publisher** [Edit](#edit) artifact keyed by `versionCode`, uploaded with the same scope and Edit model as a release upload (not the Reporting service). Despite being functionally coupled to Vitals, it is architecturally a publisher Edit upload, so gplay surfaces it under `releases` (`releases upload --mapping`, `releases mappings upload`), never under the read-only `vitals` namespace.

_Avoid_: filing a Mapping under `vitals` — `vitals` is read-only reporting; a Mapping is a publisher Edit upload.

### Internal App Sharing
A Google Play distribution channel (the `internalappsharingartifacts` resource) for uploading an APK or AAB and getting back a **private, shareable download link** that an authorized tester follows into the Play Store to install — bypassing tracks, releases, and the Edit lifecycle entirely. A QA/preview workflow, **not** a Release. gplay surfaces it under `releases sharing upload` ([ADR-0030](docs/adr/0030-android-publisher-long-tail-surfaces.md)): the channel distinction lives in the `sharing` noun, the gesture stays the canonical `upload` (the API methods are `uploadapk`/`uploadbundle`). APK vs AAB is auto-detected by extension, with a `--format apk|bundle` override.

_Avoid_: calling it a "release" (it publishes to no track and creates no [Release](#release)) or folding it under a track-keyed surface.

### Sharing artifact
The `InternalAppSharingArtifact` resource an [Internal App Sharing](#internal-app-sharing) upload returns: `{ downloadUrl, certificateFingerprint, sha256 }`. The `downloadUrl` is the shareable install link (the field the human view leads with); `certificateFingerprint` is the SHA-256 of the signing certificate; `sha256` is the artifact's content hash. Passed through verbatim on `--output json` (ADR-0003).

### Device tier config
An app-scoped, **immutable** Android Publisher resource (`applications.deviceTierConfigs`) describing device-targeting criteria for tiered content delivery: named **device groups** (each a set of device selectors over RAM, device IDs, SoCs, system features), an ordered **device tier set** (tiers by descending priority level), and **user country sets**. Created once with a server-assigned int64 `deviceTierConfigId`; read by id or listed (newest first). Lives **outside** the Edit lifecycle, like the [Data Safety declaration](#data-safety-declaration) and the [Recovery](#recovery-recovery-action). The API exposes only create/get/list — **no update/patch/delete** — so a create can never overwrite an existing config. gplay namespace: `device-tiers` (`create` / `view` / `list`), [ADR-0030](docs/adr/0030-android-publisher-long-tail-surfaces.md).

_Avoid_: filing it under `tracks` (it joins no Edit) or `apps` (that namespace is the local registry; a config is created server-side, so `create` is honest, not `add`).

### Recovery (Recovery action)
An **app recovery action** (Google's `apprecovery` resource): a targeted incident-response remediation that pushes users impacted by a bad release back to a safe app version via a remote in-app update. It has its own `appRecoveryId` and a **draft → active → canceled** lifecycle, independent of the [Edit](#edit) model (no `editId`), and is addressed by package + `versionCode`. gplay surfaces it under the top-level `recovery` namespace ([ADR-0030](docs/adr/0030-android-publisher-long-tail-surfaces.md)): `create` (a harmless draft), `list`, then the production-impacting `deploy` (activate — `--confirm`), `cancel` (irreversible — `--confirm`), and `add-targeting` (widen the audience — `--confirm`). There is no `recovery view` — the API exposes only `list`.

_Avoid_: synonyms like "rollback", "remediation", or "hotfix" — the canonical noun is **Recovery**; and filing it under `releases` (it is non-Edit, the inverse of why [Mapping](#mapping) lives there).

### Targeting (of a Recovery)
The audience selector of a [Recovery](#recovery-recovery-action): which app **versions** (a versionCode list/range) and which **users** (all-users, CLDR regions, and/or Android SDK levels) the remediation applies to. Set at `create`; afterwards it can only be **widened** via `recovery add-targeting` — the API's `addTargeting` is **append-only** (it never narrows or removes). To shrink the blast radius you must `cancel` and recreate.

_Avoid_: implying `add-targeting` can narrow or replace targeting (it is append-only) — that is why the verb is `add-targeting`, not `set`.

### Expansion file (OBB)
A **legacy** Google Play mechanism for distributing >150 MB of assets outside the APK, as an `.obb` sidecar attached to a specific APK `versionCode`. Backed by Google's `edits.expansionfiles` resource: an Android Publisher [Edit](#edit) artifact keyed by `apkVersionCode` and a **type** (`main` or `patch`), uploaded with the same scope and Edit model as a release upload — structurally a sibling of a [Mapping](#mapping). gplay surfaces it under `releases` (`releases expansion-files upload/set/view`), never under `edits` (the Edit is plumbing). Each APK has up to two expansion files (a main and an optional patch); each is either its own uploaded file (`fileSize`) or a reference to another APK's (`referencesVersion`) — the two are mutually exclusive. **Legacy**: superseded by Play Asset Delivery for AABs; only APK-based apps use it.

_Avoid_: treating it as an `edits`-namespace concept (the Edit is plumbing), or confusing the expansion **patch type** (`--type patch`) with the HTTP **PATCH** method — the API's `update` (PUT) and `patch` (PATCH) both write the one field `referencesVersion`, so gplay folds them into a single `set` verb ([ADR-0030](docs/adr/0030-android-publisher-long-tail-surfaces.md)).

### Generated APK
An APK Google Play **generated and signed** from an uploaded App Bundle — a split, standalone, or universal APK (plus asset-pack slices and recovery modules) — exposed read-only via the `generatedapks` resource. Application-scoped at `/applications/{packageName}/generatedApks/{versionCode}`, **outside the [Edit](#edit) lifecycle** (no `editId`), like [Internal App Sharing](#internal-app-sharing) and [Recovery](#recovery-recovery-action). The list response groups artifacts **by signing key** (`certificateSha256Hash`); each carries an opaque [Download ID](#download-id). gplay surfaces it under `releases generated` (`list` / `download`), [ADR-0034](docs/adr/0034-generated-apks-binary-download-to-file.md).

_Avoid_: calling it "the AAB" or "my APK" — it is the **served, Play-signed** artifact, not the developer's upload; filing it under `edits` (it joins no Edit); or assuming `generated list` opens a read-only Edit the way [`releases list`](#release) does (it does not — the GET is direct).

### Download ID
The opaque token (`downloadId`) Play assigns each [Generated APK](#generated-apk) in a `generatedapks.list` response — the **sole handle** `generatedapks.download` accepts to fetch that artifact's bytes. It is **not** a filename or URL and is not guaranteed stable across list calls; re-list to refresh it. gplay: the positional of `gplay releases generated download <downloadId>`.

_Avoid_: treating a Download ID as a download URL or a stable artifact name — it is an opaque, per-listing handle consumed only by `download`.

### Order
A Google Play purchase record identified by an **order ID** (e.g. `GPA.1234-5678-9012-34567`) — the receipt a buyer holds for a one-time or subscription purchase. Backed by the `orders` resource of the [Android Publisher API](#android-publisher-api), keyed by package: read with `orders.get` / `orders.batchget`, refunded with `orders.refund`. gplay surfaces it as `gplay orders view <orderId>...` (read) and `gplay orders refund <orderId> --confirm` (the money-moving write — [ADR-0031](docs/adr/0031-orders-commerce-reads-and-gated-refund.md)). The **canonical admin/runtime boundary example**: looking up an order *by order ID* is an [Admin API](#admin-api) diagnostic (a human or agent holds the ID from a complaint or payout report — no device token), whereas real-time **purchase-token verification** (`purchases.products` / `purchases.subscriptionsv2`) is a runtime surface, excluded by nature (ADR-0026).

_Avoid_: conflating an **Order** (admin record, order ID) with a **purchase token** (device-issued, runtime, ephemeral) or a **voided purchase** (the polled anti-fraud feed `purchases.voidedpurchases.list` — admin, in scope, but deferred to a future commerce PRD, not part of #245).
