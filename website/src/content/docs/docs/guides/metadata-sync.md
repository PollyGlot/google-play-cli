---
title: Metadata sync
description: Keep Google Play store listings and images in git with gplay — pull the live store front, validate offline, preview with dry-run, and apply additively.
sidebar:
  order: 3
---

`gplay metadata` treats your store front as code: per-locale listing text
and store images live in a directory you commit, review, and apply. The
[metadata model](/docs/concepts/metadata-model/) page covers the semantics;
this is the working loop.

## First sync: pull what's live

```sh
gplay metadata pull                  # text listings → ./metadata
gplay metadata images pull           # store images  → ./metadata/<locale>/images
git add metadata && git commit -m "snapshot store front"
```

`pull` writes one directory per locale with `title.txt`,
`short_description.txt`, `full_description.txt`, optional `video.txt` — the
same names `fastlane supply` uses, so an existing fastlane tree drops in.

## Edit, validate, preview

```sh
$EDITOR metadata/en-US/full_description.txt

# Offline checks: field limits (30/80/4000 chars), locales, file shape.
gplay metadata validate

# Exactly what would change, with no HTTP call.
gplay metadata apply --dry-run
```

## Apply

```sh
gplay metadata apply
```

`apply` is **additive**: it upserts the locales and fields present on disk
and never deletes anything by omission. A locale live on Play but absent
locally is reported, not touched. To actually delete online-only locales,
opt in with `--prune` (it still refuses to remove the app's
default-language listing).

Remember the missing-vs-empty rule: deleting a *file* means "stop managing
this field"; an *empty file* means "clear this field online".

## Images

```sh
gplay metadata images list                    # live slots and hashes
gplay metadata images validate                # offline: types, extensions
gplay metadata images apply --dry-run         # diff by content hash
gplay metadata images apply
```

Images reconcile per slot (`icon`, `featureGraphic`, screenshot galleries…)
by SHA-256: identical content and order → no-op; any difference → the slot
is cleared and re-uploaded in filename order. Removing online-only images
from a managed slot is the separate destructive mode
`images apply --prune`, which requires `--confirm`.

## In CI

A metadata job fits naturally next to your release job:

```yaml
- run: gplay metadata validate
- run: gplay metadata apply --dry-run --output json
- run: gplay metadata apply
```

## Related

- [The metadata model](/docs/concepts/metadata-model/)
- [`gplay metadata` reference](/docs/reference/metadata/)
- [Fastlane migration](/docs/guides/fastlane-migration/)
