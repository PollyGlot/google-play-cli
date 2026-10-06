---
title: Migrating from fastlane supply
description: "Every fastlane supply (upload_to_play_store) option mapped to its gplay command and flag, side-by-side lanes for the common jobs, and the behavior differences to plan for: draft production releases, Edits, exit codes, JSON output and the metadata tree."
---

gplay covers the release and store-listing work of
[`fastlane supply`][fl-docs] (the action also called `upload_to_play_store`)
as one static binary, with no Ruby in the CI image. Most of a migration is
mechanical: one supply run becomes one or a few gplay commands. This page
maps every supply option, then shows the common lanes side by side, then
lists the differences that change behavior.

The option list comes from supply's
[`options.rb`][fl-options] (40 options, fastlane `master` at
`c8daeb6`); the reference table is the
[Parameters section of the supply docs][fl-params]. Every gplay command and
flag below exists in gplay 2.x: run `gplay <command> --help` to see the full
interface.

:::caution[Production defaults to a draft]
supply's `release_status` defaults to `completed`
([`options.rb`][fl-options]), so `--track production` publishes to every user.
`gplay releases upload --track production` creates a **draft** release that
reaches no user, unless you pass `--complete` or `--staged <fraction>`, and
both of those require `--confirm` on production
([ADR-0002](https://github.com/PollyGlot/google-play-cli/blob/main/docs/adr/0002-safe-production-defaults.md)).
On every other track, an upload is `completed` at 100%, like supply.
:::

## Option by option

The 40 options of supply, in the order `options.rb` declares them. "No
equivalent" means gplay has no flag for it today; the notes give the reason
or the workaround.

### Identity and authentication

| supply option | gplay | Notes |
| --- | --- | --- |
| `package_name` | `--package <name>` on every command, or `gplay init --package <name>` once | `init` writes a Project pin (`.gplay/config.json`) so commands run in the repo default to that package. |
| `json_key` | `--service-account <path>`, the `GPLAY_SERVICE_ACCOUNT` variable, or `gplay auth login --service-account <path>` | Service-account keys only: gplay refuses a credentials JSON whose `type` is not `service_account`, whereas supply also accepts other Google credential files ([`options.rb`][fl-options]). `login` stores the key as an Account in the OS keystore; in CI, use the variable. |
| `json_key_data` | `--service-account '<json>'` or `GPLAY_SERVICE_ACCOUNT='<json>'` | Both take a path or the inline JSON content. |
| `key` | No equivalent | P12 keys. Deprecated in supply in favor of `json_key` ([`options.rb`][fl-options]); create a JSON key for the same service account. |
| `issuer` | No equivalent | Only meaningful with a P12 `key`, deprecated the same way. |
| `root_url` | No equivalent | gplay only talks to Google's API endpoints; there is no override for a proxy or a mock server. |
| `timeout` | `--timeout <duration>` (global) | Per request, e.g. `--timeout 2m`. Defaults to 60s for control-plane calls and no limit for uploads. supply also sets its API client to 5 retries ([`client.rb`][fl-client]); gplay does not retry unless you pass `--retry N` (global). |

### Artifacts and releases

| supply option | gplay | Notes |
| --- | --- | --- |
| `aab` | `gplay releases upload <app.aab> --track <track>` | The artifact is a positional argument. |
| `apk` | `gplay releases upload <app.apk> --track <track>` | Format detected from the extension (`--format apk` overrides). APK upload is experimental: Google requires an AAB for new apps. |
| `aab_paths` | No equivalent | `releases upload` takes one artifact and writes a release holding only that versionCode, so two uploads in one explicit Edit do not add up to one release: a release with several artifacts cannot be built with gplay today. |
| `apk_paths` | No equivalent | Same as `aab_paths`. |
| `skip_upload_aab`, `skip_upload_apk` | Not needed | Each gplay surface is its own command: to skip the binary, do not run `releases upload`. |
| `track` | `--track <track>` (`--from <track>` on `releases promote`) | Required: there is no default track, where supply defaults to `production` ([`options.rb`][fl-options]). Any Closed track name works. |
| `release_status` | `--draft`, `--complete` or `--staged <fraction>` on `releases upload` and `releases promote`; `gplay releases halt` for `halted` | `inProgress` is a staged rollout (`--staged`). Without a flag, gplay uses `draft` on production and `completed` elsewhere. |
| `rollout` | `--staged <fraction>` on `releases upload` / `releases promote`; `gplay releases rollout --track <track> --staged <fraction>` for a release already on the track | A rollout of `1` completes the release in supply ([`options.rb`][fl-options]); in gplay that is `gplay releases complete --track <track>` (or `--complete` at upload). On production, add `--confirm`. |
| `version_code` | `--version-code N` on `releases promote`, `rollout`, `complete`, `halt`, `resume` and `releases mappings upload` | Picks the release (or the artifact) to act on when a track holds more than one. `--release-name <name>` is the other selector. |
| `version_name` | No equivalent | gplay names a new release after its versionCode; supply uses `version_name` ([`uploader.rb`][fl-uploader]). Rename the release in the Play Console if the name matters. |
| `version_codes_to_retain` | No equivalent | gplay writes a release holding only the uploaded versionCode. |
| `in_app_update_priority` | `--update-priority <0..5>` on `releases upload` and `releases promote` | Without the flag, `upload` leaves the field unset (Google's default, `0`) and `promote` carries the source release's priority over. |
| `check_superseded_tracks` | Not needed | Deprecated in supply: "Google Play does this automatically now" ([`options.rb`][fl-options]). |
| `deactivate_on_promote` | Not needed | Deprecated in supply for the same reason. |
| `ack_bundle_installation_warning` | Not needed | Google deprecated the parameter: "The installation warning has been removed" ([`edits.bundles.upload`](https://developers.google.com/android-publisher/api-ref/rest/v3/edits.bundles/upload)). |

### Promotion

| supply option | gplay | Notes |
| --- | --- | --- |
| `track_promote_to` | `gplay releases promote --from <track> --to <track>` | Promotes the latest release on `--from`, same versionCode, no re-upload. |
| `track_promote_release_status` | `--draft`, `--complete` or `--staged <fraction>` on `releases promote` | supply defaults to `completed`; gplay defaults to `draft` when `--to production`, `completed` elsewhere. |

### Release notes (changelogs)

| supply option | gplay | Notes |
| --- | --- | --- |
| `skip_upload_changelogs` | Omit `--release-notes` / `--release-notes-dir` | `releases upload` sends notes only when asked. `releases promote` carries the source release's notes over unless you pass new ones. |

supply reads changelogs from `<locale>/changelogs/<versionCode>.txt`, falling
back to `<locale>/changelogs/default.txt`
([changelogs docs][fl-changelogs]). gplay does not read that tree: it takes
`--release-notes "<text>"` (the app's default language) or
`--release-notes-dir <dir>`, a flat directory of `<locale>.txt` files with an
optional `default.txt`. The [changelog lane](#release-notes-per-version-code)
below converts one into the other.

### Store front (metadata and images)

| supply option | gplay | Notes |
| --- | --- | --- |
| `metadata_path` | `--dir <path>` on `metadata apply` and `metadata images apply` | gplay defaults to `./metadata`; supply to `fastlane/metadata/android` ([`options.rb`][fl-options]). Pass `--dir fastlane/metadata/android` to use your tree as is ([below](#the-metadata-tree)). |
| `skip_upload_metadata` | Do not run `gplay metadata apply` | Listings (title, descriptions, video) are their own command. |
| `skip_upload_images` | `gplay metadata images apply --type phoneScreenshots --type sevenInchScreenshots ...` | `--type` restricts the run to the image types you list, so list only the screenshot types. |
| `skip_upload_screenshots` | `gplay metadata images apply --type icon --type featureGraphic ...` | The inverse: list only the singular image types. |
| `sync_image_upload` | Default behavior | gplay always compares images by content hash and uploads only what changed. Without this option, supply deletes and re-uploads ([images docs][fl-images]). |

### Review and validation

| supply option | gplay | Notes |
| --- | --- | --- |
| `validate_only` | `gplay edits begin`, your write commands, `gplay edits validate`, then `gplay edits discard` | `edits validate` runs Google's commit checks on the open Edit without publishing. For an offline preview of a single command, `--dry-run`. |
| `changes_not_sent_for_review` | `--changes-not-sent-for-review` | On every command that commits an Edit, including `gplay edits commit`. |
| `rescue_changes_not_sent_for_review` | No equivalent | supply retries the commit with or without the parameter when Google's error asks for it ([`client.rb`][fl-client]). gplay sends the commit as asked and reports the API error; re-run with or without `--changes-not-sent-for-review`. For changes already in review, `--changes-in-review cancel\|error` chooses what the commit does. |

### Debug symbols

| supply option | gplay | Notes |
| --- | --- | --- |
| `mapping` | `--mapping <mapping.txt>` on `releases upload` | Uploaded in the same Edit as the artifact. For a `native-debug-symbols.zip`, use `gplay releases mappings upload <file> --version-code N --type nativeCode`. |
| `mapping_paths` | `gplay releases mappings upload <file> --version-code N`, once per file | Each file attaches to one versionCode. |

### Expansion files (OBB)

supply picks up `.obb` files found next to the APK
([OBB docs][fl-obb]); gplay uploads them explicitly. Both gplay commands are
experimental.

| supply option | gplay | Notes |
| --- | --- | --- |
| `obb_main_references_version` | `gplay releases expansion-files set --version-code N --references-version M` | Points the APK at another versionCode's main file. To upload a new file: `gplay releases expansion-files upload <file.obb> --version-code N`. |
| `obb_main_file_size` | Not needed | supply needs it next to the references version; the API leaves the size unset on a referencing entry ([`ExpansionFile`](https://developers.google.com/android-publisher/api-ref/rest/v3/edits.expansionfiles)). |
| `obb_patch_references_version` | `gplay releases expansion-files set --version-code N --references-version M --type patch` | Same as main, `--type patch`. |
| `obb_patch_file_size` | Not needed | Same as main. |

## Common lanes, side by side

Each pair does the same job. `GPLAY_SERVICE_ACCOUNT` holds the
service-account JSON and the package comes from `gplay init`; add
`--package <name>` otherwise.

### Upload an AAB to internal

```sh
# fastlane
fastlane supply --aab app-release.aab --track internal

# gplay
gplay releases upload app-release.aab --track internal --mapping mapping.txt
```

### Promote between tracks

```sh
# fastlane
fastlane supply --track internal --track_promote_to beta

# gplay
gplay releases promote --from internal --to beta
```

### Staged production rollout

```sh
# fastlane: upload at 10%, then widen, then complete
fastlane supply --aab app-release.aab --track production --rollout 0.1
fastlane supply --track production --rollout 0.5
fastlane supply --track production --rollout 1

# gplay
gplay releases upload app-release.aab --track production --staged 0.1 --confirm
gplay releases rollout --track production --staged 0.5 --confirm
gplay releases complete --track production --confirm
```

If the rollout goes wrong, `gplay releases halt --track production` freezes
it and `gplay releases resume --track production --confirm` picks it up
again.

### Metadata and images from a directory

```sh
# fastlane: text, images and screenshots, no binary
fastlane supply --skip_upload_apk --skip_upload_aab \
  --metadata_path fastlane/metadata/android

# gplay: preview, then publish (Listings and images go live at once)
gplay metadata validate --dir fastlane/metadata/android
gplay metadata apply --dir fastlane/metadata/android --dry-run
gplay metadata apply --dir fastlane/metadata/android --confirm
gplay metadata images apply --dir fastlane/metadata/android --confirm
```

To start from what is live instead, `fastlane supply init` downloads the
store front ([docs][fl-docs]); `gplay metadata pull` and
`gplay metadata images pull` do the same into `./metadata` (or `--dir`).

### Release notes

```sh
# fastlane: read from <locale>/changelogs/<versionCode>.txt
fastlane supply --aab app-release.aab --track beta

# gplay: one text for the default language, or one file per locale
gplay releases upload app-release.aab --track beta \
  --release-notes "Bug fixes and performance improvements."
gplay releases upload app-release.aab --track beta \
  --release-notes-dir distribution/whatsnew
```

`distribution/whatsnew` holds `en-US.txt`, `fr-FR.txt` and so on, plus an
optional `default.txt` for the default language.

### Release notes per version code

To keep your existing `changelogs/` files, build the flat directory gplay
expects for the versionCode you ship, with supply's fallback order:

```sh
VERSION_CODE=1042
META=fastlane/metadata/android
NOTES=$(mktemp -d)
for dir in "$META"/*/; do
  locale=$(basename "$dir")
  for f in "$dir/changelogs/$VERSION_CODE.txt" "$dir/changelogs/default.txt"; do
    if [ -f "$f" ]; then cp "$f" "$NOTES/$locale.txt"; break; fi
  done
done
gplay releases upload app-release.aab --track beta --release-notes-dir "$NOTES"
```

## Behavior differences

### Production is a draft until you say otherwise

supply publishes with `release_status: completed` unless told otherwise
([`options.rb`][fl-options]). gplay never reaches production users by default:
an upload or a promotion to production is a `draft` unless you pass
`--complete` or `--staged`, and those need `--confirm` on production, or the
command stops with exit `3` naming the missing flag. A pipeline that relied
on supply's default needs `--complete --confirm` (or `--staged <fraction>
--confirm`) added. See [Tracks and releases](/docs/concepts/tracks-and-releases/).

### One Edit per command, or one you hold open

A supply run does its uploads, metadata, images and changelogs in one Edit
([`uploader.rb`][fl-uploader]). Each gplay write command opens its own
**Edit** and commits it (an implicit Edit), discarding it on failure. To
publish several changes atomically, hold one explicit Edit open:

```sh
gplay edits begin
gplay metadata apply --dir fastlane/metadata/android --confirm
gplay releases upload app-release.aab --track beta --release-notes-dir "$NOTES"
gplay edits validate
gplay edits commit
```

`edits begin` needs a Project (`gplay init`), since it pins the Edit in
`.gplay/`. See [The Edits model](/docs/concepts/edits/).

### Exit codes instead of error text

supply stops a lane with a Ruby error raised through `UI.user_error!`
([`uploader.rb`][fl-uploader]), so telling a transient failure from a
terminal one means reading the message. gplay exits with a semantic code: `40`
(API 5xx) and `50` (network) are worth a retry, the rest are not, and
`gplay exit-codes` prints the table. With `--output json`, the error also
carries a diagnostic `code` and a `retryable` bit. See
[Exit codes](/docs/concepts/exit-codes/).

### JSON output mirrors the API

`--output json` prints the Google Play API response as is, on stdout, with
logs on stderr: a script reads the same fields the
[API reference](https://developers.google.com/android-publisher/api-ref/rest)
documents, through `jq`. In a pipe or in CI, gplay picks JSON on its own. See
[Output formats](/docs/concepts/output-formats/).

### The metadata tree

gplay reads supply's tree as is: point `--dir` at `fastlane/metadata/android`.
Within each locale directory the file names are the same:

- `title.txt`, `short_description.txt`, `full_description.txt`, `video.txt`.
- `images/icon.png`, `images/featureGraphic.png`, `images/tvBanner.png` and
  `images/promoGraphic.png` for the singular images.
- `images/phoneScreenshots/`, `sevenInchScreenshots/`, `tenInchScreenshots/`,
  `tvScreenshots/` and `wearScreenshots/` for screenshots, in filename order.

The differences:

- **`changelogs/` is ignored.** Release notes belong to the release, not to
  the Listing: pass them to `releases upload` (see
  [the changelog lane](#release-notes-per-version-code)).
- **Sync is additive.** A locale or an image on Play but absent from disk is
  left alone; `--prune` deletes it. A missing field file leaves the live value
  untouched, and an empty one clears it.
- **Images are compared by content.** An unchanged slot is skipped; supply
  re-uploads every image unless `sync_image_upload` is set
  ([images docs][fl-images]).
- **`promoGraphic` is managed.** supply's code only handles `featureGraphic`,
  `icon` and `tvBanner` as singular images ([`supply.rb`][fl-supply]).
- **A real publish needs `--confirm`.** Listings and images go live when the
  Edit commits; `--dry-run` shows the per-locale delta first, and
  `gplay metadata validate` checks Google's length limits offline.

See [The metadata model](/docs/concepts/metadata-model/) and the
[metadata sync guide](/docs/guides/metadata-sync/).

## What you gain

- No Ruby runtime or `bundle install` in CI: one binary, fast cold start.
- `--dry-run` on every write, and `gplay auth doctor` to check the
  credential wiring before the first release.
- The rest of the Play Console in the same binary: reviews, testers, Data
  Safety, team permissions, vitals.

If you hit a migration pitfall this page does not list,
[open an issue](https://github.com/PollyGlot/google-play-cli/issues).

## Related

- [CI/CD integration](/docs/guides/ci-cd/)
- [Tracks and releases](/docs/concepts/tracks-and-releases/)
- [Authentication](/docs/concepts/authentication/)

[fl-docs]: https://docs.fastlane.tools/actions/supply/
[fl-params]: https://docs.fastlane.tools/actions/supply/#parameters
[fl-changelogs]: https://docs.fastlane.tools/actions/supply/#changelogs-whats-new
[fl-images]: https://docs.fastlane.tools/actions/supply/#images-and-screenshots
[fl-obb]: https://docs.fastlane.tools/actions/supply/#expansion-files-obb
[fl-options]: https://github.com/fastlane/fastlane/blob/c8daeb65536c4a5266e37c1cbff9c21d1f83622a/supply/lib/supply/options.rb
[fl-uploader]: https://github.com/fastlane/fastlane/blob/c8daeb65536c4a5266e37c1cbff9c21d1f83622a/supply/lib/supply/uploader.rb
[fl-client]: https://github.com/fastlane/fastlane/blob/c8daeb65536c4a5266e37c1cbff9c21d1f83622a/supply/lib/supply/client.rb
[fl-supply]: https://github.com/fastlane/fastlane/blob/c8daeb65536c4a5266e37c1cbff9c21d1f83622a/supply/lib/supply.rb
