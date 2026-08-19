---
title: Authentication & accounts
description: "How gplay resolves credentials: service accounts, named Accounts, the five-step precedence order, and the auth doctor diagnostic."
sidebar:
  order: 1
---

gplay authenticates with a Google Cloud **service account**: it reads the
service-account JSON, signs a JWT, exchanges it for a short-lived OAuth2
access token, and uses that token for all Google Play Developer API calls.

## Accounts

An **Account** is a named credential profile registered in gplay: the local
registration of one service-account JSON, under a human-friendly name.
Multiple Accounts can coexist (different apps, different orgs, dev vs CI),
and exactly one is **active** at a time.

```sh
gplay auth login --service-account ./sa.json   # register + set active
gplay auth list                                # all Accounts, active marked
gplay auth status                              # the active Account
gplay auth logout                              # remove an Account
```

The credential itself lives in your **OS keystore** (Keychain on macOS,
Secret Service on Linux, Credential Manager on Windows), with a `0600`
fallback file where no keystore is available. The config file only records
which Accounts exist and which is active, never the private key.

## Credential resolution precedence

When a command needs a credential, the first match wins:

1. `--service-account <path-or-json>` flag
2. `--account <name>` flag (selects a stored Account)
3. `GPLAY_SERVICE_ACCOUNT` env var (path **or inline JSON**)
4. `GPLAY_ACCOUNT` env var (name of a stored Account)
5. The Account marked active in the gplay config

If nothing resolves, the command exits with code `10` and points at
`gplay auth login`.

:::tip[CI]
In CI, skip `auth login` entirely: store the JSON content as a secret and
expose it as `GPLAY_SERVICE_ACCOUNT`. Inline JSON means no temp file and no
private key written to disk. See the [CI/CD guide](/docs/guides/ci-cd/).
:::

## Absent vs. invalid credentials

The two failure modes are deliberately kept apart:

- **Absent**: no source is configured. A benign state: commands that need a
  credential exit `10` with a pointer to `auth login`, but `gplay auth
  status` reports "No active account" and exits `0`, and `apps list` (local
  registry) still works.
- **Invalid**: a credential *was* provided but its bytes are unusable
  (malformed JSON, missing required field, unreadable file). Always an
  error: exit `10` with the underlying cause, on every command, including
  `auth status`, which never masks a corrupt credential as "no account".

## `gplay auth doctor`

The diagnostic command runs four checks in order, stopping at the first hard
failure: JSON well-formed → token mints → `androidpublisher` scope present →
a real `edits.insert`/`edits.delete` round-trip against your package. It
exists because the most common failure is *"service account created but
never invited on the app in Play Console"*, and that only surfaces on a
real API call.

## Related

- [Service account setup](/docs/getting-started/service-account/)
- [Configuration cascade](/docs/concepts/configuration/)
- [`gplay auth` reference](/docs/reference/auth/)
