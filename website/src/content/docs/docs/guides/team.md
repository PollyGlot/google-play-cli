---
title: Team management
description: Manage Play Console users and per-app permission grants with gplay — friendly permission aliases, frozen role bundles, and an explicit gate for admin access.
sidebar:
  order: 6
---

`gplay team` manages your **Developer account** (the Play Console
organisation): its members (*users*) and their per-app access (*grants*).
It's the one gplay surface keyed by the organisation rather than a package.

## Users — account-wide membership

```sh
gplay team users list
gplay team users add dev@example.com --role release-manager
gplay team users set dev@example.com --permissions reply-reviews
gplay team users remove dev@example.com
```

## Grants — per-app access

```sh
gplay team grants list --package com.example.myapp
gplay team grants set dev@example.com --package com.example.myapp --role reviewer
gplay team grants remove dev@example.com --package com.example.myapp
```

`grants set` is an **upsert**: gplay reads the member's current grants, then
creates or updates as needed. With `--dry-run --output json` it reports the
resolved verb (create/update) and the permission diff (current → desired)
before you commit to anything.

## Permissions without memorising Google enums

Google expresses permissions as `CAN_*` enums in two near-parallel families
(account-wide `_GLOBAL` and per-app). gplay layers two friendlier forms on
top:

- **Aliases** — scope-independent names like `release-production` or
  `reply-reviews`, resolved to the right enum family for the command's
  scope. Raw `CAN_*` enums are always accepted too, so nothing is ever
  un-grantable.
- **Role bundles** — frozen presets: `viewer`, `reviewer`,
  `tester-manager`, `release-manager`, `admin`. *Frozen* means a bundle's
  membership only changes by an explicit, versioned gplay release — never
  silently when Google adds enums. Money-related capabilities (financial
  data, orders) are deliberately excluded from every bundle and must be
  granted as explicit permissions.

The whole vocabulary is introspectable offline — no credentials needed:

```sh
gplay team permissions                 # aliases + bundles, account scope
gplay team permissions --scope app     # the per-app enum family
```

## The admin gate

Granting admin is never silent. An admin-conferring `add` or `set` (via
`--role admin` or a permission set including the all-permissions enum)
requires the named acknowledgment flag `--grant-admin`, otherwise gplay
refuses with exit `3` and names the flag. Agents can discover the gate ahead
of time: `--dry-run --output json` emits a `requires` array listing the
safety flags the live write needs.

## Related

- [Exit codes](/docs/concepts/exit-codes/) — exit `3`, the resolvable refusal
- [`gplay team` reference](/docs/reference/team/)
