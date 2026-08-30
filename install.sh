#!/usr/bin/env sh
# Install script for gplay — the Google Play Developer CLI.
#
# Usage:
#   curl -fsSL https://gplay.sh/install | sh
#
# Environment variables:
#   GPLAY_INSTALL_DIR        — directory to install into (default: $HOME/.local/bin)
#   GPLAY_VERSION            — version to install (default: latest)
#   GPLAY_INSTALL_NO_VERIFY  — set to 1 to SKIP checksum verification (prints a
#                              prominent warning). The only bypass for the
#                              fail-closed checksum gate — use for air-gapped or
#                              mirrored installs only. Any other value verifies.

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

# Verification is fail-closed: any state where the archive cannot be verified
# aborts the install — a missing checksums.txt, a checksums.txt with no entry
# for this archive, or a checksum mismatch. This closes the downgrade-by-
# omission attack, where an adversary who can influence the download path simply
# withholds the checksum to turn the old "warn and continue" into a silent
# unverified install. GPLAY_INSTALL_NO_VERIFY=1 is the single, greppable bypass.
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
  # Exact field match on the archive name, not a regex over the line: the name
  # contains dots, and a loose pattern would let an attacker-supplied
  # checksums.txt satisfy the lookup with an entry for a different artifact.
  # goreleaser writes "<sha256>  <name>"; a leading "*" marks binary mode.
  expected="$(awk -v a="$archive" '$2 == a || $2 == "*" a { print $1 }' "$tmp/checksums.txt")"
  entries="$(printf '%s\n' "$expected" | grep -c . || true)"
  if [ "$entries" != "1" ]; then
    die "expected exactly one checksum entry for $archive in checksums.txt, found $entries — refusing to install unverified. Set GPLAY_INSTALL_NO_VERIFY=1 to bypass (air-gapped/mirrored installs only)."
  fi

  # Resolve the digest tool up front. Piping `shasum | awk` would swallow a
  # missing shasum (the pipeline exits on awk's status), leaving an empty digest
  # that surfaces as a bogus "mismatch" instead of the real cause.
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

"$INSTALL_DIR/$bin_name" version || true

# -- Post-install tip: companion agent skills --------------------------------
# The reliable channel for the "install gplay" -> agent flow (ADR-0028 §5):
# an agent that just ran this script sees the skills exist instead of stopping
# at the bare binary. Needs Node/npx, the one place gplay does.
log "tip: add the companion agent skills with 'gplay install-skills' (needs Node/npx)"
