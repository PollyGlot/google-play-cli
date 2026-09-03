#!/usr/bin/env bash
# Blast-radius guard for the rolling Discovery refresh PR, shared by the two
# places allowed to merge it without a human: the revision-only auto-merge in
# discovery-watch.yml, and the verdict-merge in discovery-verdict.yml. Both
# re-derive the blast radius from the PR's own file list rather than trusting
# whatever produced the label, because the two are computed at different times
# against a force-pushed branch.
#
# Usage: discovery-blast-radius.sh <pr> [extra-allowed-regex]
#
# The baseline allow-list is the generated files only: the snapshot directory
# and the schema index derived from it. A caller that legitimately expects one
# more file passes it as an extra alternative (the triage routine commits
# docs/COVERAGE.md on the branch, see docs/agents/discovery-triage.md step 5).
# GH_TOKEN must be in the environment.
set -euo pipefail

PR="${1:?usage: discovery-blast-radius.sh <pr> [extra-allowed-regex]}"
EXTRA="${2:-}"

allowed='docs/discovery/|internal/schemaindex/schema_index\.json$'
if [ -n "$EXTRA" ]; then
  allowed="$allowed|$EXTRA"
fi

stray=$(gh pr diff "$PR" --name-only | grep -vE "^($allowed)" || true)
if [ -n "$stray" ]; then
  echo "::error::Refusing to auto-merge: the diff touches files outside the Discovery snapshot."
  printf '%s\n' "$stray"
  exit 1
fi
echo "Blast radius clean: only the expected generated files are in the diff."
