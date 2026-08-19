---
title: The metadata model
description: The on-disk metadata tree, additive sync semantics, and content-hash image reconciliation that keep Google Play store listings in git.
sidebar:
  order: 7
---

`gplay metadata` keeps your store front in git: per-locale **listings**
(title, descriptions, promo video) and **store images** (icon, feature
graphic, screenshots) live as plain files, pulled from and applied to Google
Play.

## The metadata tree

One directory per locale; one `.txt` file per listing field; an `images/`
subdirectory for store assets:

```txt
metadata/
  en-US/
    title.txt
    short_description.txt
    full_description.txt
    video.txt
    images/
      icon.png
      featureGraphic.png
      phoneScreenshots/
        01-home.png
        02-detail.png
  fr-FR/
    title.txt
    ...
```

The file names match `fastlane supply`, so an existing fastlane metadata
tree is a drop-in, minus fastlane's redundant `android/` segment and minus
`changelogs/` (release notes belong to `releases upload`, not metadata).
Plain text keeps a 4000-character description human-editable with
line-by-line git diffs.

Google's limits (title 30, short description 80, full description 4000
characters) are enforced **offline** by `gplay metadata validate`, before
any API call.

## Missing vs. empty

Within a locale, a **missing** field file and an **empty** one mean
different things to `apply`:

- missing → "I don't manage this field, leave the online value alone"
- empty → "clear this field online"

`pull` writes a file only for non-empty online fields, so a `pull` followed
by an unmodified `apply` is a no-op.

## Additive sync

`metadata apply` only ever **upserts** the locales and fields it finds on
disk. Anything live on Play but absent locally is left untouched, never
deleted by omission. Deletion is opt-in with `--prune`, which still refuses
to remove the app's default-language listing.

This stance exists because the mirror alternative ("disk is the sole truth")
turns a partial `pull` + `apply` into a data-loss event.

## Images: reconciliation by content hash

The images API has no caller-assigned keys: Google identifies each image by
a server-assigned ID and a SHA-256 of its content. So `metadata images
apply` reconciles each **slot** (a `(locale, type)` pair) by content:

- **Singular slots** (`icon`, `featureGraphic`, `tvBanner`, `promoGraphic`)
  hold one file, named by type.
- **Gallery slots** (`phoneScreenshots`, `sevenInchScreenshots`,
  `tenInchScreenshots`, `tvScreenshots`, `wearScreenshots`) are directories
  whose files, **sorted by filename, define the display order** (the API
  carries no order field, so display order is upload order).

If a gallery's local sequence matches the live one by content hash, nothing
happens; if it differs in content *or* order, gplay clears the slot and
re-uploads in order. Accepted extensions: `.png`, `.jpg`, `.jpeg`.

## Typical workflow

```sh
gplay metadata pull                 # snapshot live listings into ./metadata
git add metadata && git commit      # review like any other change
gplay metadata validate             # offline checks (limits, locales, files)
gplay metadata apply --dry-run      # preview exactly what would change
gplay metadata apply                # upsert to Google Play
```

See the [metadata sync guide](/docs/guides/metadata-sync/) for the complete
walkthrough including images.

## Related

- [`gplay metadata` reference](/docs/reference/metadata/)
- [Fastlane migration](/docs/guides/fastlane-migration/)
