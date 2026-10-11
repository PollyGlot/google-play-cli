# `gplay self update`

gplay has no command that downloads and replaces its own binary.

## Why this is out of scope

A self-updater is a binary that fetches code from the network and overwrites
itself: a security surface (download, verification, atomic replace, rollback,
permission handling on every OS) that every install channel already covers,
each in its own idiom:

```bash
brew upgrade --cask gplay                                        # Homebrew
curl -fsSL https://gplay.sh/install | sh                         # install script, checksum-verified
go install github.com/PollyGlot/google-play-cli/cmd/gplay@latest # go install
```

A self-update would also fight those channels: once a Homebrew-installed
binary replaces itself, `brew` tracks a version that no longer exists on disk.
In CI the pipeline pins the version, and a silent in-place upgrade is exactly
what a pin forbids.

The other distribution channels named in the same request (Scoop, Chocolatey,
APT/RPM) stay open on that issue; only `self update` is out of scope here.

## Prior requests

- #524: "Parking: extra distribution channels (Scoop, Chocolatey, APT/RPM,
  Docker image, `gplay self update`)"
