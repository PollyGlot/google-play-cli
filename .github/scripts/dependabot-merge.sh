#!/usr/bin/env bash
set -euo pipefail

dry_run=false
only=""
for arg in "$@"; do
  case "$arg" in
    --dry-run) dry_run=true ;;
    *) only="$arg" ;;
  esac
done

cd "$(git -C "$(dirname "$0")" rev-parse --show-toplevel)"
summary="${GITHUB_STEP_SUMMARY:-/dev/stdout}"

say() { echo "- $*" | tee -a "$summary" >/dev/null; echo "$*" >&2; }

eligibility() {
  local body moves move_re
  # shellcheck disable=SC2016 # the backticks are literal Markdown, not a substitution
  move_re='from `?v?[0-9]+(\.[0-9]+)*`? to `?v?[0-9]+(\.[0-9]+)*'
  body=$(gh pr view "$1" --json body --jq .body)
  moves=$(printf '%s\n' "$body" |
    grep -E '^(Bumps|Updates) ' |
    grep -oE "$move_re" |
    tr -d '`v' || true)
  [ -n "$moves" ] || { echo "announces no version move"; return; }
  while read -r _ from _ to; do
    if [ "${from%%.*}" != "${to%%.*}" ]; then
      echo "major bump $from -> $to"; return
    fi
  done <<<"$moves"
  echo ok
}

if [ -n "$only" ]; then
  echo "#$only: $(eligibility "$only")"
  exit 0
fi

prs=$(gh pr list --author app/dependabot --state open --json number,isDraft,mergeStateStatus \
  --jq 'sort_by(.number) | .[] | select(.isDraft | not) | "\(.number) \(.mergeStateStatus)"')
[ -n "$prs" ] || { say "No open Dependabot PR."; exit 0; }

eligible=()
while read -r pr state; do
  verdict=$(eligibility "$pr")
  if [ "$verdict" = ok ]; then
    eligible+=("$pr $state")
  else
    say "#$pr left for the maintainer: $verdict."
  fi
done <<<"$prs"

merged=""
for entry in "${eligible[@]}"; do
  read -r pr state <<<"$entry"
  [ "$state" != BEHIND ] || continue
  flag=()
  [ "$dry_run" = false ] || flag=(--dry-run)
  if scripts/merge-pr.sh "${flag[@]}" "$pr"; then
    say "#$pr merged$([ "$dry_run" = false ] || echo ' (dry run)')."
    merged="$pr"
    break
  fi
  say "#$pr not merged yet: merge-pr.sh refused (see the log)."
done

for entry in "${eligible[@]}"; do
  read -r pr state <<<"$entry"
  [ "$pr" != "$merged" ] || continue
  if [ "$state" = BEHIND ] || [ -n "$merged" ]; then
    if [ "$dry_run" = true ]; then
      say "#$pr would be brought up to date with main."
    elif gh pr update-branch "$pr" >/dev/null; then
      say "#$pr brought up to date with main; the next run merges it once green."
    else
      say "#$pr could not be updated (conflict?): left for the maintainer."
    fi
  fi
done
