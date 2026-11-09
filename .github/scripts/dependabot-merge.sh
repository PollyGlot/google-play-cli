#!/usr/bin/env bash
# Land the routine Dependabot PRs without a human, one step per run.
#
# Called by .github/workflows/dependabot-merge.yml on a schedule. Each run:
#   1. squash-merges at most one eligible PR whose head contains main and whose
#      required checks are green, through scripts/merge-pr.sh (same gates as a
#      maintainer merge);
#   2. brings every other eligible PR that is behind main up to date, so its CI
#      runs now and the next run can merge it.
# One merge per run because a merge puts every other PR behind main again, and
# merge-pr.sh refuses a behind head.
#
# Eligible means opened by Dependabot, not a draft, and every version move it
# announces keeps its major. Below 1.0 a minor may break too, but golang.org/x/*
# and most actions here live at 0.x and every such bump has been merged as is:
# CI is the gate for those. A major bump, a PR announcing no version, or a red
# check stays open for the maintainer, and the job summary says which and why.
#
# Usage:
#   dependabot-merge.sh                 # act
#   dependabot-merge.sh --dry-run       # report decisions, change nothing
#   dependabot-merge.sh --dry-run <pr>  # report the eligibility of one PR
# GH_TOKEN must be a token whose pushes trigger CI (the gplay App token in the
# workflow): an update made with GITHUB_TOKEN would start no check run.
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

# Prints "ok" or the reason the PR is not eligible. Dependabot announces each
# move on a line of its own: "Bumps [x](...) from A to B." for a single update,
# "Updates `x` from A to B" per package of a group. Release notes quoted below
# them also say "from ... to ...", hence the anchoring on the line start.
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
  # After a merge every remaining PR is behind, whatever state was read before.
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
