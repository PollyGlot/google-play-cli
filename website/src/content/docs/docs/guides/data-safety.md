---
title: Data Safety declarations
description: "Version your Google Play Data Safety declaration as a CSV in git and push it with gplay compliance datasafety: validated offline, gated by --confirm."
sidebar:
  order: 7
---

The **Data Safety declaration** states what user data your app collects, how
it's shared, and your security practices. It is a **publication gate**:
Google blocks releases when it's stale, so it bears on every app, not just
monetised ones.

## The model: one CSV, versioned in git

The declaration travels as the same import/export **CSV** the Play Console
uses. gplay adopts it verbatim rather than re-modelling Google's evolving
schema. Export it once from the Play Console (App content → Data safety),
commit it, and from then on the repo is your source of truth:

```txt
compliance/
  data-safety.csv
```

## Validate and push

```sh
# Offline structural validation: no network, no credentials.
gplay compliance datasafety validate

# Rehearse: validates, resolves the target, reports what would be sent.
gplay compliance datasafety set --dry-run

# The real write: replaces the live declaration, hence the gate.
gplay compliance datasafety set --confirm
```

`set` runs `validate` implicitly first, so a structurally invalid CSV never
reaches the network. The real write **requires `--confirm`**, because a wrong
declaration can block your releases or misstate your data practices, and
`CI=true` does *not* auto-confirm.

The default file is `./compliance/data-safety.csv`; point elsewhere with
`--file`.

## Why there is no `pull`

The Developer API exposes **only a write** for Data Safety: there is no
read endpoint. gplay can set the declaration but never show you the live
one; only the POST itself validates contents end-to-end. That single API
fact shapes the whole command: no `pull`, no diff, and an offline-only
dry-run. (This differs from [metadata sync](/docs/guides/metadata-sync/),
where pull/diff/apply is possible.)

## When to re-push

Re-run `set --confirm` whenever the CSV changes, typically when a new SDK
changes your data collection, or when Google evolves the form. Keeping the
CSV in the repo means the change is reviewed like any other code change.

## Related

- [`gplay compliance` reference](/docs/reference/compliance/)
