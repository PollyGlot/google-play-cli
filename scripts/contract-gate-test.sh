#!/usr/bin/env bash
# Offline test harness for contract-gate.sh: feeds it synthetic golden diffs
# through GOLDEN_DIFF (no git, no network) and asserts which PRs it lets
# through. Runs in the Contract workflow before the real check.
#
# Usage: bash scripts/contract-gate-test.sh
set -euo pipefail

gate="$(cd "$(dirname "$0")" && pwd)/contract-gate.sh"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

fail=0
pass_count=0

# expect <pass|fail> <label> <diff> <title> <body>
expect() {
	local want=$1 label=$2 got
	if GOLDEN_DIFF=$3 PR_TITLE=$4 PR_BODY=$5 bash "$gate" >/dev/null 2>&1; then got=pass; else got=fail; fi
	if [ "$got" = "$want" ]; then
		pass_count=$((pass_count + 1))
	else
		echo "FAIL: $label: gate said $got, want $want" >&2
		fail=1
	fi
}

hunk_header=$'--- a/cmd/gplay/testdata/surface.golden\n+++ b/cmd/gplay/testdata/surface.golden\n@@ -120,0 +121 @@'
frozen_flag="$hunk_header"$'\n+frozen flag "releases upload" --foo type=string default=""'
frozen_default="$hunk_header"$'\n-frozen flag "releases upload" --track type=string default=""\n+frozen flag "releases upload" --track type=string default="internal"'
frozen_exit="$hunk_header"$'\n-frozen exit 4 meaning="Denied" retry-safe="no"'
experimental_flag="$hunk_header"$'\n+experimental flag "games achievements list" --foo type=string default=""'

expect pass "empty diff" "" "feat: x" ""
expect pass "experimental-only change" "$experimental_flag" "feat(games): add --foo" ""
expect fail "new flag on a frozen leaf, plain title" "$frozen_flag" "feat(releases): add --foo" "Adds a flag."
expect fail "changed default on a frozen leaf" "$frozen_default" "fix(releases): default to internal" ""
expect fail "exit code change" "$frozen_exit" "fix: drop exit 4" ""
expect pass "breaking marker" "$frozen_flag" "feat!: add --foo" ""
expect pass "breaking marker with scope" "$frozen_flag" "feat(releases)!: add --foo" ""
expect pass "ADR reference in the body" "$frozen_flag" "feat(releases): add --foo" $'Adds a flag.\n\nDecided in ADR-0048.'
expect fail "bang outside the type position" "$frozen_flag" "feat(releases): add --foo!: now" ""
expect fail "ADR reference in the title only" "$frozen_flag" "feat(releases): add --foo (ADR-0048)" ""
expect fail "breaking marker on a second title line" "$frozen_flag" $'feat(releases): add --foo\nfeat!: x' ""

# The title is data, never code: a hostile title must neither run nor pass.
marker="$work/pwned"
expect fail "hostile title" "$frozen_flag" "\$(touch $marker)\`touch $marker\`" ""
if [ -e "$marker" ]; then
	echo "FAIL: hostile title was executed" >&2
	fail=1
fi

if [ "$fail" -ne 0 ]; then
	echo "contract-gate-test: FAILED" >&2
	exit 1
fi
echo "contract-gate-test: OK ($pass_count checks)."
