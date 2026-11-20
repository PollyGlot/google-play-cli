# ADR-0055: Play API behaviours the client absorbs

## Status

accepted (2026-11-20)

amended (2026-11-20): implementation decisions moved from code comments

## Context

The Google APIs gplay speaks (Android Publisher, Play Developer Reporting,
Play Games Services Publishing, Play Custom App, Cloud Storage) carry
constraints that neither the method name nor the request schema makes
obvious: an enum value the server refuses, a parameter it silently
reinterprets, a field that must be echoed in the body. Each one shaped a
piece of `internal/play`. These decisions lived in code comments; the
comment purge (the `scripts/lint/comments.py` ratchet) gathers them here.

This ADR is a catalogue, not a single decision. Each entry states what Play
does, what gplay does about it, and why. It grows one subsection per resource
or family of calls; a new entry goes under the resource it concerns, and a
resource with none yet gets its own subsection. Where Discovery is the
source, the entry quotes it so the next snapshot refresh can be checked
against it.

Related catalogues: the upload protocol is [ADR-0056](./0056-media-upload-protocol.md),
response caps and pagination [ADR-0057](./0057-bounded-responses-and-pagination.md),
failure classification [ADR-0058](./0058-api-failure-classification.md), and
JSON wire fidelity (int64 strings, explicit zeros) the implementation notes of
[ADR-0003](./0003-json-passthrough.md).

## Decision

### Addressing and path parameters

- **Discovery names a path parameter after its collection**, not after what
  it holds: `developersId`, `usersId` and `grantsId` carry the developer
  account id, the member's email and the package; `appsId` carries the
  package on the Reporting API. gplay fills them under Discovery's names, since
  the URL template comes from the registry.
- **Play App Signing accepts a numeric Play Console app ID in place of the
  package** (its `name` path parameter takes either). gplay passes it as a
  plain path segment and reports such a target as `resource.kind = app`, not
  as a package ([ADR-0023](./0023-json-error-envelope.md)).
- **A Cloud Storage object name is one path segment.** GCS keys contain
  slashes (`reviews/reviews_<pkg>_<month>.csv`), so the whole name is
  percent-encoded into a single segment rather than joined raw.

### Wire encoding

- **A `format: byte` field is base64 in proto3 JSON.** gplay types it as
  `[]byte` (App Signing's `pemCertificate`), which `encoding/json` encodes to
  exactly that shape, and hands over the raw file bytes.

### Declarative replace bodies

- **A full-replace write clears with an explicit empty array, never `null`
  or an absent key.** `edits.testers.update` replaces the whole
  `googleGroups` list and rejects `null`, so a nil list is sent as `[]`
  (the `--clear` semantics). The team write bodies (`users.patch`,
  `grants.patch`) carry their permission arrays without `omitempty` for the
  same reason: an omitted array would leave the field untouched and turn the
  clear into a no-op.
- **`tracks.update` replaces the whole releases array.** A verb that changes
  one release rebuilds the body from the raw `tracks.get` JSON and patches
  only its target, so the other releases and the fields gplay does not model
  survive (DESIGN §3, Rollout state machine).

### App details

- **An empty `defaultLanguage` on a 2xx is treated as an API failure.** Every
  app has one; accepting the empty value would send release notes keyed by an
  empty language. The guard sits where a second call depends on the value
  (the listing read that follows); `apps details view`, which reads
  `edits.details.get` alone, reports the body as the API sent it.

### App Signing

- **Only self-hosted Cloud KMS keys are reachable.** The `appsigning` methods
  (`enrollApp`, `rotateAppSigningKey`) serve organisations that keep key
  custody in Cloud KMS; enrolling with a Google-generated key and rotating a
  Google-managed key are Play Console only, so gplay offers neither.
- **`EnrollAppRequest` is a oneof** (`enrollExistingApp` or `enrollNewApp`).
  The command validates the flag combination itself so a wrong mix is a usage
  error, not an API shape error.
- **`KEY_ROTATION_REASON_UNSPECIFIED` is refused by the API** (Discovery:
  "Cannot be used"), so it is not among the `--reason` choices.

### Hosted apps (`appstoreappsreview`)

- **`fileType` is required on a policy declaration upload, and `DOCUMENT` is
  its only meaningful value** (the other member is the `UNSPECIFIED` zero), so
  gplay always sends `DOCUMENT`.
- **`images:upload` declares `image/*`.** gplay sniffs the media type from the
  file's leading bytes, never from the extension and never as
  `application/octet-stream`: the endpoint polices the announced type, so a
  wrong one is rejected after the whole transfer, and an operator's screenshot
  without a suffix is still a PNG. `edits.images.upload` behaves the same
  ([ADR-0013](./0013-image-slot-reconciliation.md)).

### Expansion files

- **Exactly one of `fileSize` and `referencesVersion` is set**: the APK has
  its own uploaded file, or points at another APK's. `fileSize` is
  output-only, so `expansionfiles.update` sends `referencesVersion` alone.

### Generated APKs

- **The `Unprotected*` lists appear only when the app uses automatic
  protection**, and their artifacts are downloadable all the same, so the
  flattened listing includes them.

### Listings and Store images

- **Inside an open Edit, a 404 on a locale means that language has no
  Listing**, not that the package is unknown: the Edit's creation already
  proved the package. A failed `listings` write or `images.list` is tagged
  with its locale so the message names the language; the exit code and the
  JSON error envelope stay those of the API error.

### One-time products (`monetization.onetimeproducts`)

- **Offer and purchase-option states.** `ACTIVE`, `INACTIVE` and `CANCELLED`
  are the targets a catalog file may declare, each reached by one unary verb
  (`:activate`, `:deactivate`, `:cancel`); purchase options share `DRAFT`,
  `ACTIVE` and `INACTIVE`. No verb reaches `DRAFT`, the state of a resource
  never activated. Play alone sets `INACTIVE_PUBLISHED`, on purchase options it
  migrated automatically: off sale, but still exposed through the Play Billing
  Library, so gplay reads it and never declares it.
- **`:cancel` is irreversible** (pending pre-orders are cancelled too), so a
  plan carrying one is gated behind `--confirm`.
- **The one-time state requests echo the resource identity** their schema
  lists, while the subscription siblings accept an empty body; gplay sends the
  listed fields on the one-time requests rather than rely on the server
  tolerating their absence.
- **`-` is a wildcard** for `productId` and `purchaseOptionId` on the offers
  list, so the whole app's offers are one paginated walk instead of one per
  purchase option.
- **A batch body takes at most 100 requests** (Discovery: "up to 100
  elements"), so gplay splits batches into calls of that size.
- **The v2 patch lives under lowercase `onetimeproducts/`** while every read
  uses `oneTimeProducts/`. The asymmetry is Google's; gplay follows the
  snapshot verbatim and a test pins it.

### Subscriptions (`monetization.subscriptions`)

- **`basePlans.migratePrices` repeats the path identity in its body**
  (`packageName`, `productId`, `basePlanId`), so gplay echoes them. Discovery
  says "Must be equal to" the resource fields for `packageName` and
  `productId`; `basePlanId` is echoed the same way.

### Orders and voided purchases

- **`voidedpurchases.list` refuses a `startTime` older than 30 days**
  (Discovery: "cannot be older than 30 days"). The command refuses a longer
  window before any request (exit 2) rather than relaying the 400.
- **`startTime` filters on when Play's systems saw the purchase as voided**,
  not on `voidedTimeMillis`, so a record can carry a void time older than the
  window.
- **`type=0` is in-app products only, `type=1` adds subscriptions.**
- **The code enums start at 0 with a meaning** (`0` is "user" or "other"), so
  the parsed source, reason and quantity fields are pointers: an absent code renders
  blank rather than as a false `0`.
- **A window with nothing voided may answer an empty body.** It is read as an
  empty last page, not a decode error.

### App recovery

- **`AppVersionRange` includes both bounds**: every versionCode from start to
  end.

### Releases and release notes

- **A release without versionCodes is legal** (a name-only draft created
  without an artifact). `releases promote` has nothing to copy from one, so
  it refuses with exit 60, a state conflict on the source track, rather than
  misuse.
- **Two release-note entries whose locales differ only by case are
  refused.** `default.txt` therefore yields to a `<locale>.txt` that matches
  the default language case-insensitively (`en-us.txt` for `en-US`), instead
  of adding a second entry for the same locale.

### Reviews

- **The reply limit is approximate.** The Reply to Reviews guide says "at most
  350 characters", Discovery's `replyText` "approximately 350". `reviews reply`
  counts Unicode characters and warns past 350 instead of refusing (#789).
- **`reviews.list` pages at 100.** Discovery types `maxResults` with no bound;
  the guide gives "up to 100 reviews per page" against a server default of 10.
  gplay always asks for full pages and applies `--limit` after the client-side
  filters (#61, #789).
- **A 404 on `reviews.get` is an unknown or an expired review**: a valid
  review that left the API's 7-day window answers the same 404.

### System APKs

- **`DeviceSpec.supportedAbis` is ordered by preference**, so gplay sends the
  caller's order unchanged, never sorted or deduplicated.

### Team (`users`, `grants`)

- **There is no `users.get` and no `grants.list` or `grants.get`.** A User's
  Grants are a field of the User, so every read of a member, or of a Grant,
  goes through `users.list` followed to completion, then filtered.
- **Emails match case-insensitively.** Google stores the address as entered,
  but addresses are case-insensitive, so a lookup must not miss a member over
  case.

### Tracks

- **A form factor track carries a prefix** in `TrackConfig.track`
  (`wear:beta`, `automotive:beta`); a default track carries none. gplay
  qualifies the name for the requested form factor, keeps a name already
  carrying the matching prefix, and refuses one carrying another form
  factor's prefix: Play rejects the mismatch, and guessing which side the
  caller meant would create the wrong track.
- **`inAppUpdatePriority` 0 is a real priority**, so the field is a pointer:
  absent stays absent (a release without a priority sends the bytes it sent
  before the field existed, #664), and an explicit 0 reaches the wire.
- **A track release carries no timestamp.** `TrackRelease` has no publish
  date and no last-updated field (Discovery properties: `countryTargeting`,
  `inAppUpdatePriority`, `name`, `releaseNotes`, `status`, `userFraction`,
  `versionCodes`), so a read can prove a production release absent but never
  stale. The `apps audit` check is named for what the API can prove
  (`CheckNoProductionRelease`), not "no recent production release".
- **A rollout fraction renders at four decimal places, trimmed**: that covers
  Play's rollout granularity and sidesteps the float noise of a naive
  `f*100` (`0.1*100` is `10.000000000000002`).

### Play Developer Reporting (vitals)

- **`anomalies.list` has no interval parameters.** The window is the
  documented `activeBetween("start", "end")` filter, with RFC 3339 timestamps
  in UTC.
- **`sampleErrorReportLimit` accepts only 0 and 1** (Discovery: "currently
  only 0 and 1 are supported"); a larger `--sample-reports` is a usage error
  (exit 2) before any request.
- **A date-only interval bound reads as that day's midnight.** An hourly bound
  always carries its hour, hour 0 included; leaving it out turned
  `--since 6h` into the whole previous day (#775).
- **Freshness is exclusive and zoned.** A metric set's `latestEndTime` is
  exclusive (reusable verbatim as a timeline end) and expressed in the set's
  default timezone: `America/Los_Angeles` for `DAILY`, UTC for `HOURLY`.
  gplay keeps that zone next to the instant rather than reading the civil
  fields as UTC.

## Consequences

- An entry here is a constraint observed or documented upstream. When a
  snapshot refresh contradicts a quoted Discovery sentence, the entry and the
  code it justifies change together.
- A behaviour that belongs to one surface's design (a gate, a verb) stays in
  that surface's ADR; this catalogue records only what Play imposes and the
  minimal client response to it.
