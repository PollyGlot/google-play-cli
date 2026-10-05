# gplay

The canonical vocabulary of gplay, a CLI for the Google Play admin APIs. Decisions live in `docs/adr/`, conventions in `docs/DESIGN.md`; this file only defines words.

## Core

**gplay**:
The binary name of this CLI.
_Avoid_: gpc (an unrelated TypeScript CLI, and the GNU Pascal Compiler)

**Edit**:
The Google Play Developer API's transactional unit: a set of changes opened on one package and committed atomically. It is either implicit (opened and committed by a single command) or explicit (held open across commands with `gplay edits begin`).
_Avoid_: transaction, changeset

**Account**:
A service-account credential registered locally in gplay under a name, exactly one of which is active at a time.
_Avoid_: service account (the Google Cloud IAM principal an Account is the local registration of), profile

**Project**:
A repo-local pin of gplay to one Android package, so commands run inside that repo default to it. It pins a package only, never an Account.

**Accessible App**:
An app the active credential can see server-side, as Google's Reporting API lists it. Deliberately distinct from the local registry of apps gplay works on.
_Avoid_: registry, treating either set as the mirror of the other

**Finding**:
One consistency observation about one app reported by `apps audit`, emitted by a named **check**. It names drift worth acting on, never a failure of the audit itself.
_Avoid_: error

## Store presence

**Store front**:
The per-locale presentation of an app on Google Play: its Listings and its Store images. Whatever is keyed by locale belongs to it; what is keyed by app is App details, what is keyed by track is Country availability.
_Avoid_: metadata (for anything outside the per-locale axis)

**Listing**:
The store text of an app for one locale: title, short and full descriptions, and an optional promo video URL. Release notes, images and app-level details are not Listing fields.

**Metadata tree**:
The on-disk form of an app's Store front: one directory per locale, each holding one file per Listing field and an optional `images/` directory. A missing file means "unmanaged"; an empty file means "clear this field".

**Additive sync**:
gplay's reconciliation stance for the Metadata tree: what is on disk is upserted, what is only live is left alone unless `--prune` is passed.
_Avoid_: mirror sync (the stance the Monetization catalog takes instead)

**Store image**:
A binary store asset of one locale and one image type (icon, feature graphic, screenshots per form factor), identified by Google through its content hash rather than a caller-chosen key.

**Image slot**:
One (locale, image type) pair, the unit image reconciliation works on. A singular slot holds at most one image; a gallery slot holds an ordered sequence.

**App details**:
The app-global configuration block: default language and the public contact email, phone and website. Keyed by app, independent of locale and track.
_Avoid_: metadata

## Tracks and testing

**Standard track**:
One of the four tracks Google Play provisions for every app: `internal`, `alpha`, `beta`, `production`.

**Closed track**:
A testing track created by the app owner beyond the Standard tracks, the only kind gplay can create. `tracks list` labels it `custom` in its kind column; the concept is the same.
_Avoid_: custom track

**Tester**:
The test audience of one track, expressed as Google Groups only.
_Avoid_: an individual person or email address as the tester unit

**Country availability**:
The set of countries an app's artifacts reach on one track. Keyed by track and read-only through the API.
_Avoid_: app-global availability

## Releases and artifacts

**Artifact preflight**:
The local check gplay runs on an upload's file before any byte leaves the machine: the container matches the resolved format and the declared package matches the target package.
_Avoid_: validation (it never checks signatures, version codes or policy)

**Mapping**:
A ProGuard/R8 deobfuscation file uploaded to an Edit so Play can symbolicate crash stack traces. It is a publisher upload, hence under `releases`, not `vitals`.

**Expansion file (OBB)**:
A legacy `.obb` asset sidecar attached to an APK version code, of type `main` or `patch`, superseded by Play Asset Delivery.
_Avoid_: confusing the `patch` type with the HTTP PATCH method

**Generated APK**:
A signed APK that Play generates from an uploaded AAB to serve devices (split, standalone, universal, asset slices). It lives outside any Edit.
_Avoid_: the AAB you uploaded

**Download ID**:
The opaque handle of one Generated APK to download, not stable across regeneration.
_Avoid_: version code, a durable identifier

**Internal App Sharing**:
A Google Play channel that turns an uploaded APK or AAB into a private install link for testers, bypassing tracks, releases and Edits.
_Avoid_: release

**Sharing artifact**:
What an Internal App Sharing upload returns: the shareable download URL, the signing certificate fingerprint and the artifact hash.

**Device tier config**:
An immutable, app-scoped set of device-targeting criteria (device groups, tiers, country sets) for tiered content delivery, created once and named on a bundle upload.

**Recovery**:
An app recovery action: an incident-response remediation that pushes users of a bad release back to a safe version through a remote in-app update, with its own draft, active and canceled lifecycle outside the Edit model.
_Avoid_: rollback, remediation, hotfix

**Targeting**:
The audience of a Recovery: which app versions and which users it applies to. It can only be widened after creation.

**Self-hosted signing key**:
An app signing key the developer keeps in their own Google Cloud KMS rather than letting Google hold it, enrolled or rotated through `signing`.
_Avoid_: upload key (the key CI signs submissions with, a different key)

## Compliance

**Compliance**:
The family of an app's regulatory declarations, the Play Console gestures that gate publication rather than shape store presence.
_Avoid_: metadata, app details

**Data Safety declaration**:
The app-global statement of what user data an app collects and shares and how it protects it, a publication gate that the API can write but never read back.
_Avoid_: metadata

## Team

**Developer account**:
The Play Console organisation that owns apps, keyed by a numeric developer ID.
_Avoid_: account, unqualified (Account is the local credential, service account the IAM principal)

**User**:
A person who is a member of a Developer account, identified by email.
_Avoid_: account

**Grant**:
A User's access to one app, carrying app-level Permissions. It is a field of the User, not a resource of its own.
_Avoid_: Tester (an audience on a track, not access to the console)

**Permission**:
One capability a User (account-wide) or a Grant (per app) can hold, as a Google `CAN_*` value.

**Permission alias**:
A gplay name for a Permission that resolves to the app-level or account-level value depending on the command's scope.

**Role bundle**:
A frozen gplay preset that expands to a fixed set of Permissions, selected with `--role`.
_Avoid_: role (unqualified), alias (an alias names one Permission, a bundle a set)

## Monetization

**Order**:
A Google Play purchase record identified by an order ID, the receipt a buyer holds. Looking it up is admin work; verifying a purchase token is runtime work.
_Avoid_: purchase token, voided purchase

**Subscription**:
A recurring-purchase product, containing **Base plans** (billing period, prices) which contain **Offers** (trials, intro pricing).
_Avoid_: IAP

**One-time product**:
A non-recurring purchase product, managed under `gplay iap`. It exists in a v2 model (purchase options and offers) and a legacy, read-only model.
_Avoid_: IAP for subscriptions

**Monetization catalog**:
The on-disk, declarative form of an app's products: one JSON file per product, in wire format. Unlike the Metadata tree it mirrors: a live product with no file is a delete.

**Reconciliation plan**:
The set of creates, patches, deletes and state transitions an `apply` computes between a Monetization catalog and the live products.
_Avoid_: synced (for a plan refused for lack of `--confirm`)

## Alternative distribution

**Hosted app**:
An app distributed by a third-party Android app store, which submits it to Google for review. Its record must be created before anything else is done to it.
_Avoid_: third-party app, Custom app

**App store package name**:
The package name of the alternative app store a `gplay appstore` request is made for: the caller, never the app being read or submitted.
_Avoid_: package, unqualified

**Media tracking id**:
The id Google returns for a file uploaded against a Hosted app, the handle a submission cites.
_Avoid_: upload token, version code

**Hosted app submission**:
The complete, declarative state of a Hosted app sent to Google for review, immediate and irrevocable.
_Avoid_: release, track

**Publish status**:
Whether an already-reviewed Hosted app is visible in the store, reversible either way.

**Catalog app view**:
Google's read-only snapshot of one Play app as the Catalog Export for app stores presents it to an app store operator.
_Avoid_: app metadata, apps view (the developer's own app)

**Update event**:
One entry of the Catalog Export's incremental feed, saying an app was modified or deleted.
_Avoid_: change, diff, notification

## Google APIs and scope

**Admin API**:
A Google API a developer or their CI calls about their app, as opposed to a **runtime API** the app or its backend calls while serving end users. Every Play admin API is in gplay's scope; runtime APIs never are.
_Avoid_: niche (as a scope argument)

**Android Publisher API**:
The core Google Play Developer API (`androidpublisher`), owning publication, monetization, distribution extras, compliance and reviews. One of several Play admin APIs.
_Avoid_: the Play API

**Play Developer Reporting API**:
The separate Google service (`playdeveloperreporting`) for post-launch observability, the source of Vitals.

**Vitals**:
Google Play's post-launch quality signals for an app: crash and ANR rates, slow start and rendering, wakeups, error reports and anomalies.
_Avoid_: androidvitals

**Metric set**:
The unit the Reporting API exposes per vital: named metrics queryable across dimensions over a timeline.

**Reporting bucket**:
The developer's Cloud Storage bucket where Play deposits its monthly CSV exports (reviews, stats, sales). Object storage, not an API.

**Play Games Services Publishing API**:
The admin service (`gamesConfiguration`) that configures a game's achievements and leaderboards as drafts, addressed by Play Games application ID.

**Custom app**:
A private app distributed to one organisation through managed Google Play, created through the Play Custom App Publishing API.
_Avoid_: Hosted app

**Play Integrity API**:
Google's runtime API verifying that a backend call comes from a genuine app on a genuine device, the canonical surface gplay never wraps.

**Discovery snapshot**:
An offline, version-pinned copy of a Google API's Discovery document, the machine-readable description of its shape. One per service.
_Avoid_: SDK

**Schema index**:
The trimmed catalog of an API's methods and types, derived from a Discovery snapshot and embedded in the binary for `gplay schema`.
_Avoid_: Discovery snapshot (the raw on-disk source), SDK

## Output and contract

**Format**:
A shape of a command's output for one reader: `table` for humans, `json` for machines, `markdown` for documents and agents, picked with `--output`.

**Diagnostic code**:
A stable token naming which failure occurred, in the JSON error envelope.
_Avoid_: exit code (the coarse bucket several diagnostic codes share), reason (Google's upstream vocabulary)

**Error resource**:
The target of a failed call as the JSON error envelope reports it: a kind of addressing axis and an id on it.
_Avoid_: package (for a target that is not one)

**Public contract**:
The part of gplay's interface a Release promises not to break without a major version bump.

**Stability label**:
A per-command marker in help output: none for stable, `[experimental]` for still evolving, `DEPRECATED:` for a migration path.

**Public preview / GA**:
The two maturity states of gplay: Public preview (`v0.x`, breaking changes possible) and GA (`v1.0` onward, Public contract in force).

**Release**:
A new published version of the gplay CLI itself, tagged `vX.Y.Z`. Distinct from a track release, the build an app ships on a Google Play track through `gplay releases`.

**Deploy**:
A new version of the gplay.sh Worker, which serves the website and the install script.
