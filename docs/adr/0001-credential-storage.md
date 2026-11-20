# Credential storage uses the native OS keystore with a 0600 file fallback

Android development happens on macOS, Windows and Linux, so credential storage must work on all three. We store service account JSONs via `go-keyring`, which targets the macOS Keychain, Windows Credential Manager, and Linux Secret Service from a single API. When no keystore daemon is available (typically Linux headless or CI), we fall back to a `0600` file under `$XDG_CONFIG_HOME/gplay/accounts/` rather than refusing to operate.

The local config file (`~/.gplay/config.json` or `$XDG_CONFIG_HOME/gplay/config.json`) holds the list of known Accounts and the active one, but **never** the private key — the key always lives in the keystore (or the 0600 fallback).

## Status

accepted

amended (2026-11-20): implementation decisions moved from code comments

## Considered Options

- **Plain 0600 file everywhere** — rejected: a Google Play service account key grants publishing rights to every app the SA can touch; a leak via a stolen home directory is too costly. Keystore raises the bar meaningfully on multi-user / shared-dev machines.
- **Keystore only, no file fallback** — rejected: would break headless Linux and most CI runners, where no Secret Service daemon is present.
- **GCP `gcloud` keyring / ADC** — rejected: ties gplay to a `gcloud` install and forces users into Google's auth flow even when they only have a downloaded SA JSON. We want gplay to work from a raw SA JSON like `fastlane supply` does.

## Consequences

- CI never calls `gplay auth login`. CI reads the credential from the `GPLAY_SERVICE_ACCOUNT` environment variable, which accepts either a path or the JSON content inline. This stays out of the storage layer entirely.
- The 0600 fallback path is **deterministic and documented**, so users on headless Linux know exactly where their key sits and can rotate it.

## Implementation notes

- **The keyring backend keeps its own index of Account names**, under a
  reserved keyring user (`__gplay_index__`): `go-keyring` offers Set, Get and
  Delete keyed by (service, user) and no way to enumerate.
- **The backend is chosen by a Set then Delete round trip.** Either step
  failing selects the file backend: a keyring that stores but cannot delete
  would break `gplay auth logout` halfway through a credential cleanup, and
  that belongs at selection time. The probe can raise the macOS
  keychain-unlock dialog, so it runs only when a command needs a credential
  ([ADR-0020](./0020-resolution-error-surfacing.md)).
- **The backend labels (`keyring`, `file`) are output**: `gplay auth status`
  emits them in its JSON and `-v` logs `keystore: using <label> backend`, so
  they are frozen like any Public field
  ([ADR-0010](./0010-versioning-public-contract-and-ga.md)).
- How the file backend keeps its modes tight and what it warns about:
  [ADR-0062](./0062-credential-exposure.md), at rest.
