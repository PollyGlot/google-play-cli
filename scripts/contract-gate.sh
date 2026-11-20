#!/usr/bin/env bash
set -euo pipefail

golden=cmd/gplay/testdata/surface.golden

if [ -n "${GOLDEN_DIFF+set}" ]; then
	diff=$GOLDEN_DIFF
else
	cd "$(git rev-parse --show-toplevel)"
	: "${BASE_SHA:?BASE_SHA is required (the commit the PR merges into)}"
	if ! git cat-file -e "$BASE_SHA:$golden" 2>/dev/null; then
		echo "contract-gate: OK, $golden does not exist at the base yet (this PR introduces it)."
		exit 0
	fi
	if [ -n "${HEAD_SHA:-}" ]; then
		diff=$(git diff --unified=0 "$BASE_SHA" "$HEAD_SHA" -- "$golden")
	else
		diff=$(git diff --unified=0 "$BASE_SHA" -- "$golden")
	fi
fi

added=$(printf '%s\n' "$diff" | grep -E '^\+frozen ' || true)
removed=$(printf '%s\n' "$diff" | grep -E '^-frozen ' || true)

if [ -z "$removed" ]; then
	if [ -n "$added" ]; then
		echo "::notice title=Frozen surface grows::this PR adds $(printf '%s\n' "$added" | wc -l | tr -d ' ') line(s) to the frozen command surface (listed in the job log). Additions are compatible; review them as new Public contract."
		echo "Added frozen lines of $golden:"
		printf '%s\n' "$added"
	fi
	echo "contract-gate: OK, no frozen line of $golden removed or modified."
	exit 0
fi

title=${PR_TITLE:-}
body=${PR_BODY:-}

if printf '%s\n' "$title" | head -n 1 | grep -qE '^[a-z]+(\([^)]*\))?!:'; then
	echo "contract-gate: OK, the frozen surface changed and the title carries a breaking marker."
	exit 0
fi
if printf '%s\n' "$body" | grep -qE 'ADR-[0-9]{4}'; then
	echo "contract-gate: OK, the frozen surface changed and the body references an ADR."
	exit 0
fi

echo "::error title=Frozen contract changed::this PR removes or modifies frozen command surface (lines below). Mark it breaking with \"!\" in the PR title (feat!: / fix!:), or reference the ADR that decides the change (ADR-NNNN) in the PR body. A compatible change only adds: a new name next to the old one, not in its place." >&2
echo "Removed or modified frozen lines of $golden:" >&2
printf '%s\n' "$removed" >&2
exit 1
