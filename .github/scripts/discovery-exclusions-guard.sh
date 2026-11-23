#!/usr/bin/env bash
set -euo pipefail

usage='usage: discovery-exclusions-guard.sh <pr> <head-sha>'
PR="${1:?$usage}"
HEAD_SHA="${2:?$usage}"
FILE='internal/apiregistry/exclusions.go'

assert_head() {
  local now
  now=$(gh pr view "$PR" --json headRefOid --jq .headRefOid)
  if [ "$now" != "$HEAD_SHA" ]; then
    echo "::error::Refusing: the PR head is $now, not the pinned $HEAD_SHA (the branch moved)."
    exit 1
  fi
}

assert_head
patch=$(gh pr diff "$PR" --color never)
assert_head

body=$(printf '%s\n' "$patch" | awk -v f="$FILE" '
  /^diff --git / { on = ($0 == "diff --git a/" f " b/" f); next }
  on && /^(\+\+\+|---) / { next }
  on && /^[-+]/ { print }
')
if [ -z "$body" ]; then
  echo "exclusions.go untouched."
  exit 0
fi

fail() { echo "::error::Refusing: $*"; exit 1; }

if printf '%s\n' "$body" | grep -q '^-'; then
  fail "the PR removes or edits a line of $FILE; the routine may only add entries."
fi

paths=$(gh api -H 'Accept: application/vnd.github.raw' \
  "repos/{owner}/{repo}/contents/docs/discovery/paths.txt?ref=$HEAD_SHA")

ids=0
reasons=0
while IFS= read -r line; do
  content=${line#+}
  if [[ $content =~ ^[[:space:]]*$ || $content =~ ^[[:space:]]*[{]$ || $content =~ ^[[:space:]]*[}],$ ]]; then
    continue
  fi
  if [[ $content =~ ^[[:space:]]*Reason:[[:space:]]+\"runtime:[^\"]*\",$ ]]; then
    reasons=$((reasons + 1))
    continue
  fi
  if [[ $content =~ ^[[:space:]]*MethodID:[[:space:]]+\"(androidpublisher\.purchases\.[a-z0-9]+\.[a-z0-9]+)\",$ ]]; then
    id=${BASH_REMATCH[1]}
    ids=$((ids + 1))
    path=$(printf '%s\n' "$paths" | awk -F'\t' -v id="$id" '$1 == id { print $3 }')
    [ -n "$path" ] || fail "$id is not in paths.txt at $HEAD_SHA."
    [[ $path == */tokens/\{token\}* || $path == */tokens/\{purchaseToken\}* ]] || fail "$id does not act on a purchase token ($path)."
    grep -qF "\"${id%.*}." "$FILE" || fail "no sibling of $id is excluded on main yet."
    continue
  fi
  fail "unexpected line in $FILE: $content"
done <<< "$body"

[ "$ids" -eq "$reasons" ] || fail "$ids added MethodID line(s) but $reasons runtime Reason line(s) in $FILE."

echo "exclusions.go: only purchase-token runtime exclusions added."
