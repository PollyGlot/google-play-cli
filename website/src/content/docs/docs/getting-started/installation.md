---
title: Installation
description: Install the gplay CLI with Homebrew, the install script, go install, or pre-built binaries for Linux, macOS, and Windows.
sidebar:
  order: 1
---

gplay ships as a single static binary. Pick whichever method fits your
machine or CI image — they all install the same thing.

## Homebrew (macOS / Linux)

```sh
brew install PollyGlot/tap/gplay
```

## Install script

Downloads the right pre-built binary for your OS and architecture:

```sh
curl -fsSL https://raw.githubusercontent.com/PollyGlot/google-play-cli/main/install.sh | sh
```

## go install

With a Go toolchain installed:

```sh
go install github.com/PollyGlot/google-play-cli/cmd/gplay@latest
```

## Pre-built binaries

Archives for Linux, macOS, and Windows (amd64 and arm64), with checksums and
signatures, are on the
[GitHub releases page](https://github.com/PollyGlot/google-play-cli/releases).

## Verify the install

```sh
gplay version
gplay --help
```

`gplay --help` prints the live command tree — it is always the source of
truth for what your installed version supports.

## In CI

In a CI pipeline, the install script is usually the fastest option:

```yaml
- run: curl -fsSL https://raw.githubusercontent.com/PollyGlot/google-play-cli/main/install.sh | sh
```

See the [CI/CD guide](/docs/guides/ci-cd/) for a complete GitHub Actions
workflow, including credential injection and retry handling.

## Next step

[Set up a Google Cloud service account](/docs/getting-started/service-account/)
so gplay can authenticate against your Play Console account.
