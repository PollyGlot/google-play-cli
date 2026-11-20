#!/usr/bin/env bash
set -euo pipefail

usage='usage: discovery-blast-radius.sh <pr> <head-sha> [extra-allowed-regex]'
PR="${1:?$usage}"
HEAD_SHA="${2:?$usage}"
EXTRA="${3:-}"

allowed='docs/discovery/|internal/schemaindex/schema_index\.json$'
if [ -n "$EXTRA" ]; then
  allowed="$allowed|$EXTRA"
fi

assert_head() {
  local now
  now=$(gh pr view "$PR" --json headRefOid --jq .headRefOid)
  if [ "$now" != "$HEAD_SHA" ]; then
    echo "::error::Refusing to auto-merge: the PR head is $now, not the pinned $HEAD_SHA (the branch moved)."
    exit 1
  fi
}

assert_head
patch=$(gh pr diff "$PR" --color never)
assert_head

files=$(printf '%s\n' "$patch" | awk '
  function flush() { if (pending != "") print pending; pending = ""; from = to = 0 }
  /^diff --git / {
    flush()
    rest = substr($0, 12)
    n = (length(rest) - 5) / 2
    name = substr(rest, 3, n)
    if (n == int(n) && rest == "a/" name " b/" name) print name
    else pending = $0
    next
  }
  /^(rename|copy) (from|to) / {
    if ($2 == "from") from = 1; else to = 1
    if (from && to) pending = ""
    sub(/^[a-z]+ [a-z]+ /, ""); print
  }
  END { flush() }
')

stray=$(printf '%s\n' "$files" | grep -vE "^($allowed)" | grep -v '^$' || true)
if [ -n "$stray" ]; then
  echo "::error::Refusing to auto-merge: the diff touches files outside the Discovery snapshot."
  printf '%s\n' "$stray"
  exit 1
fi
echo "Blast radius clean: only the expected generated files are in the diff of $HEAD_SHA."
