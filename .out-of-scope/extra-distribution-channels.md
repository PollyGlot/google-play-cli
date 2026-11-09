# Extra distribution channels (Scoop, Chocolatey, WinGet, APT/RPM)

gplay ships through Homebrew, GitHub Releases, the checksum-verified install
script, `go install` and a container image on GHCR. It is not published to
Scoop, Chocolatey, WinGet, or a signed APT/RPM repository.

## Why this is out of scope

Every channel is a manifest or a hosted repository to keep in sync with every
release, forever. None adds a capability: each platform already has a
supported path.

```bash
curl -fsSL https://gplay.sh/install | sh                         # macOS, Linux
go install github.com/PollyGlot/google-play-cli/cmd/gplay@latest # any Go toolchain
# Windows: the zip on the GitHub Release, or the PowerShell install script
# CI: the native binary, or the GHCR image
```

No user on any of these channels has asked for one. A concrete request from a
user who cannot use the paths above reopens the discussion for that channel
alone.

## Prior requests

- #146: WinGet (folded into #524)
- #524: "Parking: extra distribution channels (Scoop, Chocolatey, WinGet, APT/RPM)"
