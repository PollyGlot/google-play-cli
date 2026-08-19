---
title: Quickstart
description: "From zero to a release upload in five commands: login, doctor, project pinning, and the core gplay release workflow."
sidebar:
  order: 3
---

This page assumes gplay is [installed](/docs/getting-started/installation/)
and you have a [service-account JSON](/docs/getting-started/service-account/)
with access to your app.

## 1. Authenticate

```sh
# Register the credential (stored in your OS keychain) and make it active.
gplay auth login --service-account ./service_account.json

# See which accounts exist and which one is active.
gplay auth list
gplay auth status
```

## 2. Verify access to your app

```sh
gplay auth doctor --package com.example.myapp
```

This round-trips a real API call and tells you exactly what's missing if the
service account isn't fully wired up.

## 3. Pin your project (optional, recommended)

```sh
cd ~/code/my-android-app
gplay init --package com.example.myapp
```

`gplay init` writes `.gplay/config.json` at the repo root. Every gplay
command run inside that tree now targets `com.example.myapp` by default, with
no more `--package` on each call. The pin is meant to be committed; see
[Configuration](/docs/concepts/configuration/).

## 4. Look around

```sh
# The release tracks and what's on them.
gplay tracks list
gplay tracks view --track production

# Recent user reviews (the API exposes the last 7 days).
gplay reviews list --stars 1-2
```

## 5. Ship something

```sh
# Upload an AAB to the internal track with localized release notes.
gplay releases upload app.aab --track internal --release-notes-dir ./whatsnew

# Promote the latest internal build to beta: same versionCode, no re-upload.
gplay releases promote --from internal --to beta

# Stage a production rollout at 10%, then advance it.
gplay releases rollout --track production --to 0.10
```

:::note[Safe by default]
Targeting `production` creates a **draft** release unless you explicitly
pass `--complete` or `--staged <fraction>`. Completing or staging a
production release additionally requires `--confirm`. Nothing reaches users
by accident. See [Tracks & releases](/docs/concepts/tracks-and-releases/).
:::

## Where to go next

- [Release flow guide](/docs/guides/release-flow/): the full
  upload → promote → rollout lifecycle.
- [CI/CD guide](/docs/guides/ci-cd/): the same flow in GitHub Actions.
- [Metadata sync](/docs/guides/metadata-sync/): keep store listings in git.
- [CLI reference](/docs/reference/): every command and flag.
