# Credential storage uses the native OS keystore with a 0600 file fallback

Android development happens on macOS, Windows and Linux, so credential storage must work on all three. We store service account JSONs via `go-keyring`, which targets the macOS Keychain, Windows Credential Manager, and Linux Secret Service from a single API. When no keystore daemon is available (typically Linux headless or CI), we fall back to a `0600` file under `$XDG_CONFIG_HOME/gplay/accounts/` rather than refusing to operate.

The local config file (`~/.gplay/config.json` or `$XDG_CONFIG_HOME/gplay/config.json`) holds the list of known Accounts and the active one, but **never** the private key — the key always lives in the keystore (or the 0600 fallback).

## Considered Options

- **Plain 0600 file everywhere** — rejected: a Google Play service account key grants publishing rights to every app the SA can touch; a leak via a stolen home directory is too costly. Keystore raises the bar meaningfully on multi-user / shared-dev machines.
- **Keystore only, no file fallback** — rejected: would break headless Linux and most CI runners, where no Secret Service daemon is present.
- **GCP `gcloud` keyring / ADC** — rejected: ties gplay to a `gcloud` install and forces users into Google's auth flow even when they only have a downloaded SA JSON. We want gplay to work from a raw SA JSON like `fastlane supply` does.

## Consequences

- CI never calls `gplay auth login`. CI reads the credential from the `GPLAY_SERVICE_ACCOUNT` environment variable, which accepts either a path or the JSON content inline. This stays out of the storage layer entirely.
- The 0600 fallback path is **deterministic and documented**, so users on headless Linux know exactly where their key sits and can rotate it.
