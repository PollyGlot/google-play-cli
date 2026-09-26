#!/usr/bin/env bash
# Blast-radius guard for the rolling Discovery refresh PR, shared by the two
# places allowed to merge it without a human: the revision-only auto-merge in
# discovery-watch.yml, and the verdict-merge in discovery-verdict.yml. Both
# re-derive the blast radius from the PR's own file list rather than trusting
# whatever produced the label, because the two are computed at different times
# against a force-pushed branch.
#
# Usage: discovery-blast-radius.sh <pr> <head-sha> [extra-allowed-regex]
#
# <head-sha> is the commit the caller is about to merge (it passes the same sha
# to `gh pr merge --match-head-commit`). `gh pr diff` can only read the live
# head, so the guard reads the head before and after the diff and refuses when
# either differs from <head-sha>: the file list it approves is then the file
# list of that exact commit, and the merge refuses any other.
#
# The baseline allow-list is the generated files only: the snapshot directory
# and the schema index derived from it. A caller that legitimately expects one
# more file passes it as an extra alternative (the triage routine commits
# docs/COVERAGE.md on the branch, see docs/agents/discovery-triage.md step 5).
# GH_TOKEN must be in the environment.
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
# Captured on its own line, not piped into grep: a failed `gh pr diff` must fail
# the guard, never read as an empty (hence clean) file list.
files=$(gh pr diff "$PR" --name-only)
assert_head

stray=$(printf '%s\n' "$files" | grep -vE "^($allowed)" | grep -v '^$' || true)
if [ -n "$stray" ]; then
  echo "::error::Refusing to auto-merge: the diff touches files outside the Discovery snapshot."
  printf '%s\n' "$stray"
  exit 1
fi
echo "Blast radius clean: only the expected generated files are in the diff of $HEAD_SHA."
