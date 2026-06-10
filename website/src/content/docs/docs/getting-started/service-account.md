---
title: Service account setup
description: Create a Google Cloud service account, link it to your Play Console, and grant it the permissions gplay needs — with gplay auth doctor to verify the result.
sidebar:
  order: 2
---

gplay authenticates to Google with a **Google Cloud service account** that
has been granted access to your Play Console app. This is a one-time setup.

## 1. Create the service account

1. In the **Google Cloud Console**, create or pick a project, then go to
   **IAM & Admin → Service accounts → Create service account**.
2. On the **Keys** tab, choose **Add key → JSON** and download the `*.json`
   file.

:::caution
Treat the JSON key as a secret — it can publish to your store listings.
Never commit it to a repository.
:::

## 2. Link it in the Play Console

1. In the **Play Console**, go to **Setup → API access**.
2. Link the Google Cloud project that owns the service account.
3. Grant the service account the permissions your workflow needs:
   - *"Release apps to production, exclude devices, and use Play App
     Signing"* — required for `releases upload`, `promote`, and the rollout
     verbs.
   - *"Reply to reviews"* — required for `reviews reply`.
   - Whatever else maps to the commands you'll run.

## 3. Verify with `gplay auth doctor`

```sh
gplay auth login --service-account ./service_account.json
gplay auth doctor --package com.example.myapp
```

`auth doctor` runs four checks in order, stopping at the first hard failure:

1. The service account JSON is present, readable, and well-formed.
2. An OAuth2 access token can be minted (the signed JWT exchange succeeds).
3. The token bears the `androidpublisher` scope.
4. For the targeted package, a real `edits.insert` + `edits.delete`
   round-trip succeeds against the Play API.

That last check catches the single most common setup error: **the service
account exists but was never invited on the app in the Play Console**. The
doctor output names the failing step and what to do about it.

## How gplay talks to Google

gplay reads the service-account JSON, signs a JWT with its private key,
exchanges it for a short-lived OAuth2 access token, and uses that token for
all Google Play Developer API calls. Tokens are minted on demand — nothing
long-lived is written to disk, and the credential itself is stored in your
OS keychain when you use `gplay auth login` (see
[Authentication & accounts](/docs/concepts/authentication/)).

## Next step

[Run your first commands](/docs/getting-started/quickstart/).
