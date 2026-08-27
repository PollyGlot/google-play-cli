#!/usr/bin/env bash
# Offline test harness for install.sh — proves the sha256 gate is fail-closed.
#
# Runs install.sh against a fake release built on disk, with a stub `curl` on
# PATH mapping URLs to local fixtures. No network, no real download, so it can
# run in CI on every PR alongside shellcheck.
#
# Usage: bash scripts/install-test.sh

set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
script="$repo_root/install.sh"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

fail=0
pass_count=0

sha256() {
  if command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | awk '{print $1}'
  else sha256sum "$1" | awk '{print $1}'; fi
}

# -- Fixture release ---------------------------------------------------------
# The archive name install.sh builds depends on the host, so mirror its own
# os/arch detection rather than hardcoding a platform.
case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) echo "install-test: unsupported host OS, skipping" >&2; exit 0 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "install-test: unsupported host arch, skipping" >&2; exit 0 ;;
esac

version="v9.9.9"
archive="gplay_9.9.9_${os}_${arch}.tar.gz"
fixtures="$work/fixtures"
mkdir -p "$fixtures/payload"
cat >"$fixtures/payload/gplay" <<'EOF'
#!/bin/sh
echo "gplay 9.9.9 (test fixture)"
EOF
chmod +x "$fixtures/payload/gplay"
tar -C "$fixtures/payload" -czf "$fixtures/$archive" gplay
good_sum="$(sha256 "$fixtures/$archive")"

printf '%s  %s\n' "$good_sum" "$archive" >"$fixtures/checksums.good.txt"
printf '%s  %s\n' "0000000000000000000000000000000000000000000000000000000000000000" "$archive" \
  >"$fixtures/checksums.bad.txt"
printf '%s  %s\n' "$good_sum" "gplay_9.9.9_other_platform.tar.gz" >"$fixtures/checksums.missing-entry.txt"
# Two entries for the same archive: ambiguous, so unverifiable.
{ printf '%s  %s\n' "$good_sum" "$archive"; printf '%s  %s\n' "$good_sum" "$archive"; } \
  >"$fixtures/checksums.duplicate.txt"

# -- curl stub ---------------------------------------------------------------
# Serves the archive always; serves whatever CHECKSUMS_FIXTURE names, or 404s
# (curl -f's exit 22) when it is empty.
mkdir -p "$work/bin"
cat >"$work/bin/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
url=""; out=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    -*) shift ;;
    *) url="$1"; shift ;;
  esac
done
case "$url" in
  *checksums.txt)
    [ -n "${CHECKSUMS_FIXTURE:-}" ] || exit 22
    src="$FIXTURES/$CHECKSUMS_FIXTURE" ;;
  *.tar.gz) src="$FIXTURES/$ARCHIVE" ;;
  *) exit 22 ;;
esac
[ -f "$src" ] || exit 22
if [ -n "$out" ]; then cp "$src" "$out"; else cat "$src"; fi
EOF
chmod +x "$work/bin/curl"

# -- Runner ------------------------------------------------------------------

# run <name> <expected-exit> <expected-stderr-substring> <checksums-fixture> [env=val ...]
run() {
  local name="$1" want_exit="$2" want_msg="$3" fixture="$4"; shift 4
  local dest="$work/dest/$name" out="$work/$name.log" rc=0
  rm -rf "$dest"; mkdir -p "$dest"
  env PATH="$work/bin:$PATH" \
      FIXTURES="$fixtures" ARCHIVE="$archive" CHECKSUMS_FIXTURE="$fixture" \
      GPLAY_INSTALL_DIR="$dest" GPLAY_VERSION="$version" \
      "$@" sh "$script" >"$out" 2>&1 || rc=$?

  local problems=""
  [ "$rc" = "$want_exit" ] || problems="exit $rc (want $want_exit)"
  if [ -n "$want_msg" ] && ! grep -qF "$want_msg" "$out"; then
    problems="$problems; stderr missing '$want_msg'"
  fi
  if [ "$want_exit" = "0" ]; then
    [ -x "$dest/gplay" ] || problems="$problems; binary not installed"
  else
    [ ! -e "$dest/gplay" ] || problems="$problems; binary installed despite failure"
  fi

  if [ -n "$problems" ]; then
    printf 'FAIL  %s: %s\n' "$name" "${problems#; }"
    sed 's/^/      | /' "$out"
    fail=1
  else
    printf 'ok    %s\n' "$name"
    pass_count=$((pass_count + 1))
  fi
}

run valid-checksum          0 "checksum OK"                     checksums.good.txt
run checksum-mismatch       1 "checksum mismatch"               checksums.bad.txt
run checksums-unavailable   1 "could not fetch checksums.txt"   ""
run no-entry-for-archive    1 "found 0"                         checksums.missing-entry.txt
run duplicate-entries       1 "found 2"                         checksums.duplicate.txt
run bypass-installs-anyway  0 "SKIPPING checksum verification"  checksums.bad.txt GPLAY_INSTALL_NO_VERIFY=1
run bypass-off-still-checks 1 "checksum mismatch"               checksums.bad.txt GPLAY_INSTALL_NO_VERIFY=0

# A host with no sha256 tool must abort, not install unverified. `command -v`
# has to come up empty, so run against a PATH built from an explicit tool list
# that omits shasum and sha256sum.
hidden="$work/hidden"
mkdir -p "$hidden"
for tool in sh bash uname mktemp tar unzip grep awk sed head printf mkdir install rm cp tr; do
  p="$(command -v "$tool" 2>/dev/null || true)"
  [ -n "$p" ] && ln -sf "$p" "$hidden/$tool"
done
ln -sf "$work/bin/curl" "$hidden/curl"
rc=0
env -i PATH="$hidden" HOME="$work" \
    FIXTURES="$fixtures" ARCHIVE="$archive" CHECKSUMS_FIXTURE="checksums.good.txt" \
    GPLAY_INSTALL_DIR="$work/dest/no-sha-tool" GPLAY_VERSION="$version" \
    sh "$script" >"$work/no-sha-tool.log" 2>&1 || rc=$?
if [ "$rc" != "1" ] || ! grep -qF "no sha256 tool found" "$work/no-sha-tool.log" \
   || [ -e "$work/dest/no-sha-tool/gplay" ]; then
  printf 'FAIL  no-sha256-tool: exit %s\n' "$rc"
  sed 's/^/      | /' "$work/no-sha-tool.log"
  fail=1
else
  printf 'ok    no-sha256-tool\n'
  pass_count=$((pass_count + 1))
fi

if [ "$fail" -ne 0 ]; then
  printf '\ninstall-test: FAILED\n'
  exit 1
fi
printf '\ninstall-test: %d checks passed\n' "$pass_count"
