#!/usr/bin/env sh

set -eu

REPO="PollyGlot/google-play-cli"
INSTALL_DIR="${GPLAY_INSTALL_DIR:-$HOME/.local/bin}"
VERSION="${GPLAY_VERSION:-latest}"

log()  { printf "\033[1;34m==>\033[0m %s\n" "$*" >&2; }
warn() { printf "\033[1;33m==>\033[0m %s\n" "$*" >&2; }
die()  { printf "\033[1;31m==>\033[0m %s\n" "$*" >&2; exit 1; }

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

if [ "$VERSION" = "latest" ]; then
  log "resolving latest release of $REPO..."
  VERSION="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
    | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)"
  [ -n "$VERSION" ] || die "could not resolve latest version (no releases yet?). Pass GPLAY_VERSION=vX.Y.Z to install a specific tag."
fi

ver_no_v="${VERSION#v}"

if [ "$os" = "windows" ]; then
  archive_ext="zip"
else
  archive_ext="tar.gz"
fi
archive="gplay_${ver_no_v}_${os}_${arch}.${archive_ext}"
url="https://github.com/$REPO/releases/download/$VERSION/$archive"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

log "downloading $url"
if ! curl -fsSL "$url" -o "$tmp/$archive"; then
  die "download failed. Is $VERSION published for $os/$arch? See $REPO/releases."
fi

if [ "${GPLAY_INSTALL_NO_VERIFY:-}" = "1" ]; then
  warn "GPLAY_INSTALL_NO_VERIFY=1 — SKIPPING checksum verification."
  warn "    The archive will be installed UNVERIFIED. Use only for trusted"
  warn "    mirrors or air-gapped installs."
else
  log "verifying checksum"
  checksums_url="https://github.com/$REPO/releases/download/$VERSION/checksums.txt"
  if ! curl -fsSL "$checksums_url" -o "$tmp/checksums.txt"; then
    die "could not fetch checksums.txt for $VERSION — refusing to install unverified. Set GPLAY_INSTALL_NO_VERIFY=1 to bypass (air-gapped/mirrored installs only)."
  fi
  expected="$(awk -v a="$archive" '$2 == a || $2 == "*" a { print $1 }' "$tmp/checksums.txt")"
  entries="$(printf '%s\n' "$expected" | grep -c . || true)"
  if [ "$entries" != "1" ]; then
    die "expected exactly one checksum entry for $archive in checksums.txt, found $entries — refusing to install unverified. Set GPLAY_INSTALL_NO_VERIFY=1 to bypass (air-gapped/mirrored installs only)."
  fi

  if command -v shasum >/dev/null 2>&1; then
    actual="$(shasum -a 256 "$tmp/$archive")"
  elif command -v sha256sum >/dev/null 2>&1; then
    actual="$(sha256sum "$tmp/$archive")"
  else
    die "no sha256 tool found (need shasum or sha256sum) — refusing to install unverified. Set GPLAY_INSTALL_NO_VERIFY=1 to bypass (air-gapped/mirrored installs only)."
  fi
  actual="$(printf '%s' "$actual" | awk '{print $1}' | tr 'A-F' 'a-f')"
  expected="$(printf '%s' "$expected" | tr 'A-F' 'a-f')"
  [ "$expected" = "$actual" ] || die "checksum mismatch (expected $expected, got $actual)"
  log "checksum OK"
fi

log "extracting"
if [ "$archive_ext" = "zip" ]; then
  unzip -q "$tmp/$archive" -d "$tmp"
else
  tar -C "$tmp" -xzf "$tmp/$archive"
fi

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

"$INSTALL_DIR/$bin_name" version || true

log "tip: add the companion agent skills with 'gplay install-skills' (needs git)"
