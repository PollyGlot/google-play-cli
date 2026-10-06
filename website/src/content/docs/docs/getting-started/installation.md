---
title: Installation
description: Install the gplay CLI with Homebrew, the install script, go install, a container image, or pre-built binaries for Linux, macOS, and Windows.
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
`checksums.txt` and fails closed**: a missing checksum file, no entry (or an
ambiguous one) for your platform, a mismatch, or no sha256 tool on the host all
abort the install before anything is written. For air-gapped or
mirrored installs where the checksum file is unreachable, set
`GPLAY_INSTALL_NO_VERIFY=1` to bypass (it prints a warning and stays greppable
in your CI config).

## go install

With a Go toolchain installed:

```sh
go install github.com/PollyGlot/google-play-cli/cmd/gplay@latest
```

## Container image

A multi-arch image (linux/amd64, linux/arm64) ships with every release on
GHCR, built from the same binaries as the archives. Tags are `vX.Y.Z`, `X.Y`
and `latest`; the entrypoint is `gplay`, on a distroless base running as a
non-root user. Forward the credential from your environment as inline JSON
(a host path does not exist inside the container):

```sh
docker run --rm -e GPLAY_SERVICE_ACCOUNT \
  ghcr.io/pollyglot/gplay:<version> tracks list --package com.example.myapp
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

The container image carries the same proofs, stored in GHCR next to it:

```sh
gh attestation verify oci://ghcr.io/pollyglot/gplay:<version> \
  -R PollyGlot/google-play-cli
cosign verify ghcr.io/pollyglot/gplay:<version> \
  --certificate-identity-regexp '^https://github.com/PollyGlot/google-play-cli/\.github/workflows/release\.yml@' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
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
