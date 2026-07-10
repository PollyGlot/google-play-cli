# App icon: a faithful live read, `sha256` as the durable handle, no cache

## Status

Accepted

## Context

Grilled from PRD [#337](https://github.com/PollyGlot/google-play-cli/issues/337)
under [ADR-0026](0026-maximal-admin-api-coverage.md) (maximal admin-API
coverage). Two read surfaces want to expose an app's store **icon**:

- **`apps view`** — the cross-resource identity card. It already merges
  `edits.details.get` + `edits.listings.get` on the default language inside one
  read-only [Edit](../../CONTEXT.md#edit) (the documented ADR-0003 envelope
  exception). Adding the icon of the default language answers "am I looking at
  the right app?" more completely.
- **`metadata images list --type`** — narrowing the per-slot summary to one
  [image slot](../../CONTEXT.md#image-slot) (e.g. `icon`) across locales.

The icon is a [Store image](../../CONTEXT.md#store-image), so it is already read
via `edits.images.list`, which returns an `Image` object per stored image:
`{id, url, sha1, sha256}`. The question this ADR settles is **what gplay
promises about that data** — which field is durable, whether gplay caches it,
and which write-safety and scope rules apply.

## Decision

1. **Faithful live read, every time.** Both surfaces read the icon from
   `edits.images.list` on each invocation, inside the same read-only Edit they
   already open. gplay never returns a remembered value; the answer always
   reflects the store's current state at call time.

2. **`sha256` is the durable content-identity handle.** The `Image.sha256` is
   the stable, content-addressed identifier gplay already reconciles images by
   ([ADR-0013](0013-image-slot-reconciliation.md)). It is the field a caller may
   persist, diff, or key a cache on.

3. **`Image.url` is a preview URL — never persist it.** The `url` is an
   ephemeral preview link with **undocumented resolution, auth, and expiry**
   semantics: it is not a stable address, may require credentials, and can stop
   resolving without notice. gplay passes it through verbatim on `--output json`
   (ADR-0003) and in the `apps view` envelope's `icon` key, but the human
   (`table`/`markdown`) views deliberately surface only `sha256`, and the docs
   state plainly that `url` must not be cached or stored. To obtain the actual
   icon **bytes**, use the existing `gplay metadata images pull`.

4. **The `apps view` `icon` key is `{"url":..,"sha256":..}`, optional.** It
   extends the ADR-0003 envelope exception: the key carries verbatim
   `edits.images` field values and is **omitted entirely** when the default
   language's icon slot is empty (missing == empty, ADR-0013). Absent icon →
   no key, no row.

5. **Full `androidpublisher` scope — there is no read-only listings scope.**
   Reading images requires the standard `androidpublisher` OAuth scope gplay
   already uses; Google exposes **no** narrower `…androidpublisher.readonly`
   scope for listings/images. So the icon read confers no new permission
   requirement beyond "the service account is invited on the app". A `403` on
   the icon read → exit `11`, `404` → `30`, `5xx` → `40`, network → `50`, via
   the shared `*api.Error` mapping.

6. **Not gated by `GPLAY_READONLY`.** Both surfaces are pure reads — they open a
   read-only Edit that is always discarded, never committed, and mutate nothing.
   Per [ADR-0024](0024-readonly-environment-policy.md) only mutating commands are
   marked; a read is exempt and keeps working under a read-only deployment.

7. **No cache in gplay — caching is the consumer's responsibility.** gplay does
   **not** store the icon (bytes, url, or sha256) between invocations, and does
   not add a cache flag or a cache directory. A caller who wants to avoid
   re-reading keys their own cache on the durable `sha256` and manages its
   freshness themselves. This keeps gplay a thin, honest window onto live store
   state: a cache would introduce a staleness contract gplay has no way to
   honor (the store can change out of band), and would be the first persistent
   state gplay owns beyond config and open Edits — a cost with no offsetting
   correctness benefit for a CLI whose whole value is a faithful read.

8. **Ships `[experimental]` first**
   ([ADR-0010](0010-versioning-public-contract-and-ga.md)): the `icon` key on
   `apps view` and the `metadata images list --type` flag are outside the
   [Public contract](../../CONTEXT.md#public-contract) until they graduate, like
   every new surface under ADR-0026.

## Why

- **The durable-handle rule is the load-bearing part.** The tempting shortcut is
  to treat `Image.url` as "the icon's address" and hand it to callers as a
  stable link. That is a latent bug: the URL's expiry/auth are undocumented, so
  a persisted url silently rots. Naming `sha256` as the one durable handle — and
  routing byte retrieval through `metadata images pull` — pins the honest
  contract before a consumer builds on the wrong field.

- **No-cache is a deliberate boundary, not an omission.** A CLI that caches a
  remote resource inherits an invalidation problem it cannot solve (the store
  mutates through the Play Console, other tools, other agents). Declining the
  cache keeps every read faithful and keeps gplay stateless beyond config.
  Consumers who genuinely need caching have the perfect key already: `sha256`.

- **Scope reality avoids a false promise.** Documenting that no listings
  `.readonly` scope exists stops a future reader from "hardening" the read with
  a narrower scope that Google does not offer — and explains why a read still
  requires the full `androidpublisher` scope.

## References

- [ADR-0003](0003-json-passthrough.md) — `--output json` pass-through and the
  `apps view` envelope exception (now optionally carrying `icon`).
- [ADR-0013](0013-image-slot-reconciliation.md) — content-hash reconciliation;
  `missing == empty` for an image slot.
- [ADR-0024](0024-readonly-environment-policy.md) — `GPLAY_READONLY` gates only
  mutating commands; these reads are exempt.
- [ADR-0034](0034-generated-apks-binary-download-to-file.md) — the sibling
  Edit-free binary read; icon **bytes** are fetched with `metadata images pull`,
  not surfaced inline.
