#!/usr/bin/env bash
# Read-only smoke test of gplay against the real Play API (#736).
#
# The offline suite only knows the rules Google documents. Some behaviour exists
# only on the live API (#731: "Only one completed release is allowed"; #741: a
# legacy list refused with "Please migrate to the new publishing API"). This
# script runs the read paths of the main namespaces against one real app and
# fails when any of them exits non-zero. GPLAY_READONLY=1 makes gplay refuse
# every write, so the run cannot change the app.
#
# Usage:
#   GPLAY_SERVICE_ACCOUNT=<path or inline JSON> scripts/live-smoke.sh <gplay binary> <package>
#
# Writes a Markdown table to $GITHUB_STEP_SUMMARY when it is set.
# Exit codes: 0 every command passed, 1 at least one failed, 2 usage error.

set -uo pipefail

if [[ $# -ne 2 || -z "${GPLAY_SERVICE_ACCOUNT:-}" ]]; then
  echo "usage: GPLAY_SERVICE_ACCOUNT=<path or JSON> scripts/live-smoke.sh <gplay binary> <package>" >&2
  exit 2
fi
gplay=$1
pkg=$2
export GPLAY_READONLY=1

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

commands=(
  "auth doctor"
  "apps view"
  "tracks list"
  "releases list --track production"
  "releases artifacts"
  "metadata list"
  "metadata pull --dir $work/metadata"
  "subscriptions pull --dir $work/subscriptions"
  "iap pull --dir $work/iap"
  "reviews list"
  "vitals crashes"
)

summary="| command | exit |"$'\n'"|---|---:|"$'\n'
failed=0
for c in "${commands[@]}"; do
  read -r -a argv <<<"$c"
  if "$gplay" "${argv[@]}" --package "$pkg" --output json >"$work/out" 2>"$work/err"; then
    code=0
  else
    code=$?
    failed=$((failed + 1))
    echo "FAIL ($code): gplay $c" >&2
    sed 's/^/  /' "$work/err" >&2
  fi
  echo "exit $code: gplay $c"
  summary+="| \`gplay ${c//$work/<tmp>}\` | $code |"$'\n'
done

if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  {
    echo "## Live read smoke"
    echo
    printf '%s' "$summary"
    echo
    echo "$failed of ${#commands[@]} command(s) failed."
  } >>"$GITHUB_STEP_SUMMARY"
fi

[[ $failed -eq 0 ]]
