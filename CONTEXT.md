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
A repo-local pinning of a gplay invocation to a specific Android package. Created by `gplay init --package com.example.myapp`, which writes `.gplay/project.json` at the repo root. Any subsequent gplay command run inside that tree (walking up to find `.gplay/`) defaults its target to that package, so `--package` becomes optional.

A Project pins a **package only** — not an Account. Account resolution stays separate (config-wide active or env/flag).

Coexists in `.gplay/` with `edit-<package>.json` (open explicit Edit, see above). `.gplay/` is meant to be committed for `project.json` and gitignored for `edit-*.json` (transient).

### Listing
The textual store-front of an app **for one locale**: `title`, `shortDescription`, `fullDescription`, and an optional promo `video` URL. Backed by Google's `edits.listings` resource. A locale that has any of these is said to "have a Listing"; `deletegroup` removes a locale's Listing entirely.

On disk, each field maps to a fastlane-named file inside the locale directory: `title.txt`, `short_description.txt`, `full_description.txt`, `video.txt` (snake_case, identical to `fastlane supply` so an existing tree is a drop-in). Google's limits — title 30, short 80, full 4000 chars — are enforced offline by `gplay metadata validate`.

Deliberately **excludes** release notes (the per-release "what's new" text): those are not a Listing field on Google Play and gplay owns them under `releases upload --release-notes[-dir]`, not under metadata. It also excludes app-level details (contact email, default language — `edits.details`), a separate app-global API resource owned by the `apps` namespace, and store images (`edits.images`), which are a separate API resource owned by the sibling `metadata images` sub-namespace (see Store image) — never a Listing field.

The canonical on-disk form of a set of Listings is the **metadata tree** (see below).

### Store front
The **per-locale** presentation of an app on Google Play: its Listings (text) and its store images (icon, feature graphic, screenshots per locale and form factor — see Store image). This is the axis the `metadata` command namespace owns, with text under the Listing commands and images under the `metadata images` sub-namespace. Deliberately distinct from **app-global** configuration — default language and contact details (`edits.details`) — which is keyed by app, not by locale, and belongs with the `apps` namespace (gplay already reads details via `apps info`), never inside the metadata tree.

The dividing question for any new store-presence surface is its **key axis**: keyed by locale → store front → `metadata`; keyed by app → **App details** → `apps`; keyed by track → **Country availability** → `tracks`.

### App details
The app-global configuration block backed by Google's `edits.details` resource: `defaultLanguage` plus the user-visible contact `email`, `phone`, and `website`. Keyed by **app** — one set per package, independent of locale (unlike a Listing) and of track (unlike Country availability). The only writable app-global surface gplay exposes today; read with the bare `apps details`, written field-by-field with `apps details set`.

Distinct from **apps info**, the cross-resource *identity card* (package + title + default language, where `title` comes from a Listing, not from details). App details is the full `edits.details` record; `apps info` is a deliberately terse "am I looking at the right app?" check.

_Avoid_: calling these fields "metadata" — metadata is the per-locale Store front.

### Store image
A binary store asset attached to a Listing, keyed by **locale and image type** (`edits.images`): the singular slots `icon`, `featureGraphic`, `tvBanner`, `promoGraphic`, and the gallery slots `phoneScreenshots`, `sevenInchScreenshots`, `tenInchScreenshots`, `tvScreenshots`, `wearScreenshots`. Part of the Store front (per-locale), so it lives under `metadata`, in a `metadata images` sub-namespace distinct from the text Listing commands — the two share the metadata tree and the Additive sync stance but reconcile differently (text upserts by field name; images diff by content, see Image slot).

Unlike a Listing field, a Store image has **no caller-assigned key**: the API identifies each image by a server-assigned `id` and a content `sha256`, and `edits.images.upload` cannot name a slot position. Reconciliation is therefore by content hash, not by field name.

### Image slot
The unit `metadata images apply` reconciles: one `(locale, image type)` pair. A **singular** slot (`icon`, `featureGraphic`, `tvBanner`, `promoGraphic`) holds at most one image; a **gallery** slot (`phoneScreenshots`, `sevenInchScreenshots`, `tenInchScreenshots`, `tvScreenshots`, `wearScreenshots`) holds an **ordered sequence** of up to Play's max (currently 8), where the **on-disk filename sort is the display order** (the `edits.images` API carries no order field — display order is upload order, the `fastlane supply` convention). gplay reconciles the local sequence (files sorted by name, by `sha256`) against the live sequence: identical → no-op; differs in content *or* order → `deleteall` + re-upload in order. This sequence-with-content-hash identity, plus `missing == empty` (see [ADR-0013](./docs/adr/0013-image-slot-reconciliation.md)), is what justifies a separate sub-namespace rather than folding into `metadata apply`.

### Metadata tree
The on-disk form of an app's Store front: a directory with one sub-directory per locale. Each locale holds one `.txt` file per Listing field plus an optional `images/` sub-directory for its Store images. Inside `images/`, a singular slot is one file named by type (`icon.png`, `featureGraphic.png`, `tvBanner.png`, `promoGraphic.png`); a gallery slot is a directory named by type (`phoneScreenshots/`, `sevenInchScreenshots/`, `tenInchScreenshots/`, `tvScreenshots/`, `wearScreenshots/`) whose files, sorted by name, are the display sequence. Accepted image extensions are `.png`, `.jpg`, `.jpeg`. It mirrors the shape `fastlane supply` reads, minus fastlane's redundant `android/` segment and minus its `changelogs/` (release notes live with `releases`, not metadata). Plain text is chosen over JSON for the text fields so a 4000-character `fullDescription` stays human-editable and produces a line-by-line git diff. The directory is the unit `gplay metadata pull` writes and `gplay metadata apply` reads (`--dir`, default `./metadata`).

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

### Format
A way of shaping a command's output for a specific reader: `table` for humans on a TTY, `json` for machines and scripts, `markdown` for documentation, chat agents, and PR comments. Selected by `--output`. The special value `auto` (the default, encoded as the empty string on the flag) lets the CLI pick based on context: `json` when stdout is not a TTY or when `CI=true`, `table` otherwise.

`json` is API pass-through (see [ADR-0003](./docs/adr/0003-json-passthrough.md)). `table` and `markdown` are human-shaped views; each command chooses how to render its data in those formats.

### Renderer
A function that turns a command's data payload into bytes for a given Format. Each command supplies its own three Renderers (one per Format), and a shared dispatcher in `internal/output` picks the right one based on the resolved Format. The dispatcher owns the "unsupported format" error path so each command stops re-implementing it.

### Release
Cutting a new published version of the **gplay CLI** itself: the versioned binaries, archives, checksums, SBOMs, cosign signature, Homebrew formula and GitHub Release. A Release is triggered by merging the rolling "release PR" maintained by release-please, which creates the `vX.Y.Z` tag; GoReleaser then builds and publishes the artifacts. A Release concerns the CLI distribution only — it never touches the gplay.sh Worker, and the Worker needs no redeploy when a new version ships (see Deploy).

### Deploy
Publishing a new version of the **gplay.sh Worker code** (the Cloudflare Worker that serves the install script behind `https://gplay.sh/install`). A Deploy is `wrangler deploy` of `deploy/gplay.sh/`, triggered only when the Worker's own code changes (`worker.js` / `wrangler.toml`) — not when `install.sh` changes (the Worker proxies it live from `main`) and not when the CLI is Released. Distinct from Release: a Deploy concerns the install-URL proxy, a Release concerns the CLI artifacts. The two are fully decoupled.

### Public contract
The subset of gplay's interface that a Release promises not to break without a major version bump: command and flag names and their semantics, exit codes, the config schema and resolution precedence, Account resolution precedence, and the guarantee that `--output json` stays API-passthrough. Deliberately **excluded**: the `table` / `markdown` layouts (human views, free to evolve), the *fields* inside the passthrough JSON (owned by the Google Play Developer API, not gplay), and stderr / log wording. Distinct from "everything the CLI does" — the Public contract is only the part under an explicit stability promise. See [ADR-0010](./docs/adr/0010-versioning-public-contract-and-ga.md).

### Stability label
A per-command lifecycle marker shown in help output, signalling how far a command's surface can be depended on: **no label** = part of the Public contract (stable); **`[experimental]`** = shipped but still evolving, outside the contract; **`DEPRECATED:`** = a compatibility path kept during a migration, not the long-term home. Lets a command ship before it is frozen.

### Public preview / GA
Two named maturity states of the gplay CLI. **Public preview** is the `v0.x` line: publicly installable and usable, an invitation to test and give feedback, with breaking changes still possible — no Public contract promise yet. **GA** (general availability, `v1.0`+) is the state where the Public contract is in force. These are communication-and-promise states, not feature-count thresholds.
