# APK upload rides `releases upload` — extension auto-detect, `edits.apks` write-only (`edits.apks.upload`)

Grilled from PRD [#118](https://github.com/PollyGlot/google-play-cli/issues/118)
under [ADR-0026](0026-maximal-admin-api-coverage.md) (maximal admin-API
coverage). `edits.apks.upload` is the legacy sibling of `edits.bundles.upload`:
it attaches an `.apk` (instead of an `.aab`) to an [Edit](../../CONTEXT.md#edit),
after which track assignment, release notes, and commit are identical. Google
has required the AAB for **new** apps since August 2021, so this surface only
serves **existing** apps still distributed as APKs — a completeness/parity
feature, deliberately outside the first-release-readiness theme.

The decisions:

- **No new command: `gplay releases upload` accepts `.apk`.** The artifact type
  is auto-detected by file extension (`.apk` → `apks.upload`, `.aab` →
  `bundles.upload`), with a `--format apk|bundle` override for pathological
  filenames. This is the exact convention `releases sharing upload` already
  ships ([ADR-0030](0030-android-publisher-long-tail-surfaces.md)) — one
  gesture, the channel/artifact distinction lives in the input, not in a new
  verb. A second command (`releases upload-apk`) would fork the release flow's
  flag surface (`--track`, `--staged`, `--release-notes*`, `--mapping`,
  `--dry-run`, `--confirm`) for zero semantic gain.

- **The rest of the pipeline is unchanged, by construction.** Everything after
  the upload step keys on the `versionCode` the API returns, which
  `apks.upload` returns exactly like `bundles.upload`. So draft-by-default on
  production ([ADR-0002](0002-safe-production-defaults.md)), the explicit-Edit
  pin reuse (#48), `--dry-run`, `GPLAY_READONLY` refusal, and release-notes
  handling apply to APK uploads without new code paths.

- **`--mapping` works for APKs unchanged.** `edits.deobfuscationfiles.upload`
  is keyed on `apkVersionCode` regardless of whether that versionCode came from
  an APK or an AAB — same wire path, no flag changes.

- **Client: a dedicated `apks` package mirroring `bundles`.** Same simple-media
  upload protocol (upload sub-host, `uploadType=media`,
  `application/octet-stream`), same explicit `ContentLength` (proxies +
  retryability), same exit-code nuance: a 400/404 on `apks.upload` maps to
  client-side validation (exit 20), mirroring the `bundles.upload` operation
  hint in the API error mapper. One package per resource is the repo's layout;
  generalizing `bundles` into a two-endpoint package would couple two resources
  for ~40 shared lines.

- **The other two `edits.apks` methods are excluded.**
  - `edits.apks.list` — parity with `edits.bundles.list`, which gplay also does
    not expose: in-Edit artifact enumeration has no CLI story (`releases list`
    shows releases; `releases generated list` shows served artifacts). Excluded
    until a concrete need appears.
  - `edits.apks.addexternallyhosted` — registers an APK hosted **outside** Play,
    restricted to EMM/managed-Play distribution. Niche-within-niche; excluded.

Consequences: `releases upload` documentation must stop implying AAB-only and
explain the legacy-APK caveat (new apps cannot use it — the API rejects APK
uploads for AAB-required apps, which gplay surfaces verbatim). `COVERAGE.md`
marks `edits.apks` upload ✅ / list + addexternallyhosted 🚫 once shipped.
