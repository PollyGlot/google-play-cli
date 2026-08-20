# Store image reconciliation: content-hash identity, `missing == empty`, explicit-only full clear

## Status

accepted

## Context

`gplay metadata images` extends the Store front (per-locale) surface from
text Listings (`edits.listings`, ADR-0011) to store images
(`edits.images`): the singular slots `icon`, `featureGraphic`,
`tvBanner`, `promoGraphic` and the gallery slots `phoneScreenshots`,
`sevenInchScreenshots`, `tenInchScreenshots`, `tvScreenshots`,
`wearScreenshots`. It reuses ADR-0011's stance — additive by default,
`--prune` to delete, one atomic Edit, online `--dry-run`, `--confirm`
gate — but **cannot** reuse its reconciliation mechanics, and that
divergence is surprising enough to record.

Two properties of `edits.images` break the text model:

1. **No caller-assigned key.** A Listing field is keyed by name
   (`title`, `fullDescription`) and the value *is* the content, so
   `listings.patch` upserts by field. An image has no such key: the API
   assigns each image a server-side `id` and a content `sha256`, and
   `images.upload` cannot name a slot position. There is nothing to
   `patch` — only "what is the set of images in this slot, by content".

2. **A slot is a set, represented on disk by a directory.** The text
   "missing ≠ empty" rule (ADR-0011 §2) relies on a *0-byte file* being
   a first-class git artifact: it commits, diffs, and round-trips. The
   image analog of "empty" is an *empty directory* — and **git does not
   track empty directories**. An "empty directory = delete every image
   in this slot" signal therefore cannot survive a `clone`/`checkout`,
   is never emitted by `pull` (which, like its text sibling, only writes
   slots that have content online), and would only ever exist as a
   hand-authored form in the exact shape git destroys — while a *stray*
   empty directory (left by an `rm`, a tool artifact) would silently
   trigger a destructive `deleteall`.

## Decision

1. **Content-hash identity, order-aware for galleries.** The unit of
   reconciliation is the **Image slot**: one `(locale, image type)`
   pair. The `edits.images` API carries no order field — display order
   is upload order — so a gallery slot is reconciled as an **ordered
   sequence**, not a set: the on-disk filename sort is the display order
   (the `fastlane supply` convention). `apply` compares the local
   sequence (files sorted by name, by `sha256`) against the live
   sequence (the API returns `sha256` per image in `images.list`). For a
   singular slot, or a gallery whose content and order already match,
   identical → no-op. A gallery that differs in **content or order** is
   reconciled by `deleteall` + re-upload in order (the only way to
   reorder, since there is no position patch); this stays one commit per
   `apply` and galleries are small (≤8). Under `--prune`, online images
   absent from the local sequence are removed; without it, the additive
   default never deletes.

2. **`missing == empty` — both mean "unmanaged".** A slot whose
   directory is **absent or empty** (or, for a singular type, whose file
   is absent) is **not managed**: its live slot on Play is left entirely
   untouched, never pruned. This deliberately **inverts** the text rule
   ("missing ≠ empty"): for images, an empty/absent directory and a
   stray empty directory must be indistinguishable and harmless, because
   git cannot tell them apart and cannot preserve the difference anyway.

3. **Full-slot deletion is explicit, never by omission.** Because
   `missing == empty`, there is no on-disk way to drive a slot to zero
   images. That is intentional: emptying a slot (e.g. unpublishing every
   screenshot of a locale) is rare and destructive and must not be
   expressible "by accident of tree shape". A dedicated, `--confirm`-d
   clear gesture (`metadata images clear`, deferred to BACKLOG) is the
   only path to zero; `apply --prune` can shrink a managed gallery but
   never empties it.

4. **Exhaustive offline validation, but Play stays the ultimate
   authority.** The `edits.images` API exposes **no** endpoint that
   returns the asset rules — dimensions, ratios, byte sizes, counts live
   only in Google's published docs and are enforced only at
   upload/commit. `metadata images validate` therefore encodes those
   rules as a **versioned, datestamped table in code** (single source,
   citing the doc URL) and checks them all offline: exact dimensions
   (`icon` 512×512, `featureGraphic` 1024×500, `tvBanner` 1280×720),
   screenshot range + ratio, format (png/jpg/jpeg), per-image byte size,
   and per-slot count (1–8). Dimensions/format are read via the stdlib
   `image.DecodeConfig` (header only, no pixel decode, no external dep —
   consistent with ADR-0007). This is deliberately strict (the intent is
   "respect the guidelines to the pixel"), and `apply` runs it as a
   fail-fast pre-check.

   The hard limit on this strictness: because the rule table is
   hand-maintained and Google can change a limit, gplay must never be
   *permanently* more restrictive than Play. `apply --no-validate`
   bypasses the offline check and lets the upload/commit be the judge,
   so a stale table can never block in CI an upload Play would accept.
   Likewise the required-slot rule (Play needs an icon, a feature
   graphic, and a minimum screenshot count to publish) is not specially
   cased: a single atomic Edit (ADR-0011 §5) means a `--prune` that
   drops a slot below Play's minimum is **rejected at commit** and the
   Edit auto-discards, store untouched. Offline `validate` is a friendly
   early failure and the *default* gate; Play's commit is the authority.

5. **A dedicated image diff schema — parallel sibling, not a shared
   envelope.** ADR-0011 §6 anticipated that image scope would "add
   records without reshaping" to the text dry-run schema. The separate
   sub-namespace decided here (Decision §2, and the Q2 command split)
   changes that premise: because `metadata apply` (text) and
   `metadata images apply` are distinct commands, a single `--dry-run`
   document is **always either text or images, never both** — so a
   shared envelope would only produce a union type with half its fields
   always null. `metadata images apply --dry-run --output json` instead
   emits its **own** schema that keeps the text schema's *shape and
   conventions* (a flat outer object, a flat counter `summary` so a CI
   gate stays one `jq` line) but uses image-native fields:

   ```json
   {"package": "...",
    "slots": [{"locale", "imageType", "op", "sha256", "position"}],
    "summary": {"upload": N, "delete": N, "reorder": N, "unchanged": N}}
   ```

   where `op ∈ {upload, delete, reorder, unchanged, untouchedSlot}`. A
   reordered gallery emits one `reorder` record for the slot (not one
   per image), plus `upload`/`delete` records for genuine adds/removes,
   so `summary` stays honest. This **deliberately supersedes** ADR-0011
   §6's "shared, no reshaping" anticipation; the two schemas are a
   learnable family, not one envelope. Like the text schema it is an
   ADR-0003 exception and falls under the Public contract once GA.

## Consequences

- The text and image surfaces share ADR-0011's *stance* (additive,
  `--prune`, atomic Edit, online dry-run, `--confirm`) but differ on the
  on-disk contract: text is "missing ≠ empty", images are
  "missing == empty". `metadata images` is a separate sub-namespace, not
  a fold into `metadata apply`, precisely so these two reconciliation
  rules never collide under one command.
- `pull` then `apply` with no edits stays a guaranteed no-op: `pull`
  writes a slot directory only for a slot that has images online, so it
  never emits the empty form, and content-hash identity makes a
  re-`apply` of identical bytes upload nothing. Since `images.list`
  returns no original filename, `pull` downloads each image's bytes
  (from the API `url`) and **synthesizes** names: singular slots as
  `<type>.<ext>`, gallery slots as `1.<ext>`…`N.<ext>` in display order
  (filename sort = order, the Q4 rule; Play's 8-image cap means no
  two-digit sort hazard, so no zero-padding). The extension is derived
  by sniffing the image bytes (PNG/JPEG magic number), not the response
  `Content-Type`. App name and per-locale title are deliberately **not**
  in filenames — the tree root already scopes one package and the path
  already carries locale + type + order (the `fastlane` convention).
- Driving a slot to zero images is not possible in this PRD's scope;
  attempting it (an empty/absent directory) is a silent no-op by design.
  The `metadata images clear` escape hatch is parked in `docs/BACKLOG.md`.
- The on-disk layout and the `missing == empty` semantics fall under the
  Public contract (ADR-0010) once GA: stable, changeable only with a
  major bump.
- **Soundness assumption (must be verified, has a fallback).** The
  content-hash model is only correct if Play stores image bytes verbatim
  — i.e. `sha256(local file) == sha256` returned by `images.list` for
  that image. If Play transcoded/recompressed store images, the remote
  hash would never match and every `apply` would re-upload every image
  forever. The first implementation slice **must** include an
  integration test (upload → `list` → assert remote `sha256` equals the
  local file's) before the fine-grained diff is relied on. If the
  assumption fails, the designed fallback is **always-replace galleries**
  (`apply` does `deleteall` + re-upload the managed local sequence rather
  than diffing) — correct but less economical. The fallback is
  preferred over a local `sha256 → image id` manifest, which would add a
  hidden third source of truth and contradict "disk and Play are the
  only two authorities".
