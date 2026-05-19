#!/usr/bin/env sh
# Install script for gplay — the Google Play Developer CLI.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/PollyGlot/google-play-cli/main/install.sh | sh
#
# Environment variables:
#   GPLAY_INSTALL_DIR  — directory to install into (default: $HOME/.local/bin)
#   GPLAY_VERSION      — version to install (default: latest)

set -eu

REPO="PollyGlot/google-play-cli"
INSTALL_DIR="${GPLAY_INSTALL_DIR:-$HOME/.local/bin}"
VERSION="${GPLAY_VERSION:-latest}"

# -- Helpers -----------------------------------------------------------------

log()  { printf "\033[1;34m==>\033[0m %s\n" "$*" >&2; }
warn() { printf "\033[1;33m==>\033[0m %s\n" "$*" >&2; }
die()  { printf "\033[1;31m==>\033[0m %s\n" "$*" >&2; exit 1; }

# -- Detect OS / arch --------------------------------------------------------

uname_s="$(uname -s)"
case "$uname_s" in
  Linux)   os="linux"   ;;
  Darwin)  os="darwin"  ;;
  MINGW*|MSYS*|CYGWIN*) os="windows" ;;
  *) die "unsupported OS: $uname_s. Use go install or download from $REPO/releases." ;;
esac

uname_m="$(uname -m)"
case "$uname_m" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) die "unsupported arch: $uname_m. Use go install or download from $REPO/releases." ;;
esac

# -- Resolve version ---------------------------------------------------------

if [ "$VERSION" = "latest" ]; then
  log "resolving latest release of $REPO..."
  VERSION="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
    | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)"
  [ -n "$VERSION" ] || die "could not resolve latest version (no releases yet?). Pass GPLAY_VERSION=vX.Y.Z to install a specific tag."
fi

# Drop the leading "v" for the archive name (matches goreleaser output).
ver_no_v="${VERSION#v}"

# -- Pick archive & extension ------------------------------------------------

if [ "$os" = "windows" ]; then
  archive_ext="zip"
else
  archive_ext="tar.gz"
fi
archive="gplay_${ver_no_v}_${os}_${arch}.${archive_ext}"
url="https://github.com/$REPO/releases/download/$VERSION/$archive"

# -- Download + extract ------------------------------------------------------

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

log "downloading $url"
if ! curl -fsSL "$url" -o "$tmp/$archive"; then
  die "download failed. Is $VERSION published for $os/$arch? See $REPO/releases."
fi

log "verifying checksum"
checksums_url="https://github.com/$REPO/releases/download/$VERSION/checksums.txt"
if curl -fsSL "$checksums_url" -o "$tmp/checksums.txt"; then
  expected="$(grep " $archive$" "$tmp/checksums.txt" | awk '{print $1}' || true)"
  if [ -n "$expected" ]; then
    actual="$(shasum -a 256 "$tmp/$archive" 2>/dev/null | awk '{print $1}' \
              || sha256sum "$tmp/$archive" | awk '{print $1}')"
    [ "$expected" = "$actual" ] || die "checksum mismatch (expected $expected, got $actual)"
  else
    warn "no checksum entry for $archive (continuing)"
  fi
else
  warn "no checksums.txt for $VERSION (continuing without verification)"
fi

log "extracting"
if [ "$archive_ext" = "zip" ]; then
  unzip -q "$tmp/$archive" -d "$tmp"
else
  tar -C "$tmp" -xzf "$tmp/$archive"
fi

# -- Install -----------------------------------------------------------------

mkdir -p "$INSTALL_DIR"
bin_name="gplay"
[ "$os" = "windows" ] && bin_name="gplay.exe"
install -m 0755 "$tmp/$bin_name" "$INSTALL_DIR/$bin_name"

log "installed $INSTALL_DIR/$bin_name"

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    warn "$INSTALL_DIR is not in your PATH. Add it to your shell rc:"
    warn "    export PATH=\"$INSTALL_DIR:\$PATH\""
    ;;
esac

"$INSTALL_DIR/$bin_name" --version || true
