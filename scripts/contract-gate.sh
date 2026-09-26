#!/usr/bin/env bash
# contract-gate.sh: a PR that changes the frozen command surface must say so.
#
# cmd/gplay/testdata/surface.golden is generated from the cobra tree (`make
# contract-update`, kept fresh by TestSurfaceGolden_isFresh) and every line
# starts with its stability. A removed line starting with "frozen" breaks the
# Public contract (ADR-0010, ADR-0042): a renamed or dropped flag, a changed
# default or type (a modification is a removal plus an addition), a removed
# leaf, an exit code. Such a PR must carry either a breaking marker in its title
# (`feat!:`, `fix(scope)!:`, which release-please reads as a major bump) or a
# reference to the ADR that decides the change (`ADR-NNNN` in the body).
#
# A pure addition (only `+frozen` lines: a new flag, a new frozen leaf, a
# graduation from experimental) is compatible: ADR-0010 promises not to break
# anything, not to add nothing, and a `feat!` there would bump the major for
# nothing. It passes, and the additions are listed in the log and as a notice
# so the reviewer still sees what joins the frozen surface. Experimental lines
# never trip the gate.
#
# Inputs, all from the environment. The workflow passes the title and body
# through `env:` and never interpolates them into a `run:` block: they are
# attacker-controlled text, and here they are only ever matched with grep.
#   PR_TITLE, PR_BODY  the pull request title and body
#   BASE_SHA           the commit to diff against
#   HEAD_SHA           optional; default: the working tree (for local runs)
#   GOLDEN_DIFF        optional; a unified diff to check instead of git (tests)
set -euo pipefail

golden=cmd/gplay/testdata/surface.golden

if [ -n "${GOLDEN_DIFF+set}" ]; then
	diff=$GOLDEN_DIFF
else
	cd "$(git rev-parse --show-toplevel)"
	: "${BASE_SHA:?BASE_SHA is required (the commit the PR merges into)}"
	# The PR that introduces the golden adds every line at once: that pins the
	# existing contract, it does not change it.
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

# `+++ b/...` and `--- a/...` headers never match: the stability is followed by
# a space, and the file path is not "frozen".
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

# Conventional Commits breaking marker: `type!:` or `type(scope)!:`.
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
