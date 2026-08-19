---
title: Installation
description: Install the gplay CLI with Homebrew, the install script, go install, or pre-built binaries for Linux, macOS, and Windows.
sidebar:
  order: 1
---

gplay ships as a single static binary. Pick whichever method fits your
machine or CI image: they all install the same thing.

## Homebrew (macOS / Linux)

```sh
brew install PollyGlot/tap/gplay
```

## Install script

Downloads the right pre-built binary for your OS and architecture:

```sh
curl -fsSL https://gplay.sh/install | sh
```

The script **verifies the downloaded archive's SHA-256 against the release
`checksums.txt` and fails closed**: a missing checksum file, no entry for
your platform, or a mismatch all abort the install. For air-gapped or
mirrored installs where the checksum file is unreachable, set
`GPLAY_INSTALL_NO_VERIFY=1` to bypass (it prints a warning and stays greppable
in your CI config).

## go install

With a Go toolchain installed:

```sh
go install github.com/PollyGlot/google-play-cli/cmd/gplay@latest
```

## Pre-built binaries

Archives for Linux, macOS, and Windows (amd64 and arm64), with checksums and
signatures, are on the
[GitHub releases page](https://github.com/PollyGlot/google-play-cli/releases).

## Verify a release

Every release ships two origin-independent proofs you can gate on before
trusting `gplay` in a pipeline: a **GitHub build-provenance attestation** over
each archive, and a **keyless cosign signature** over `checksums.txt`.

```sh
# Provenance: proves the archive was built by this repo's release workflow.
gh attestation verify gplay_<version>_<os>_<arch>.tar.gz \
  -R PollyGlot/google-play-cli

# cosign signature over checksums.txt, then the archive against it.
cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github.com/PollyGlot/google-play-cli/\.github/workflows/release\.yml@' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
shasum -a 256 -c <(grep " gplay_<version>_<os>_<arch>.tar.gz$" checksums.txt)
```

A ready-to-paste CI step that installs and verifies in one shot is in the
[CI/CD guide](/docs/guides/ci-cd/).

## Verify the install

```sh
gplay version
gplay --help
```

`gplay --help` prints the live command tree, and it is always the source of
truth for what your installed version supports.

## In CI

In a CI pipeline, the install script is usually the fastest option:

```yaml
- run: curl -fsSL https://gplay.sh/install | sh
```

See the [CI/CD guide](/docs/guides/ci-cd/) for a complete GitHub Actions
workflow, including credential injection and retry handling.

## Next step

[Set up a Google Cloud service account](/docs/getting-started/service-account/)
so gplay can authenticate against your Play Console account.
