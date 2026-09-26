#!/usr/bin/env bash
# contract-gate.sh: a PR that changes the frozen command surface must say so.
#
# cmd/gplay/testdata/surface.golden is generated from the cobra tree (`make
# contract-update`, kept fresh by TestSurfaceGolden_isFresh) and every line
# starts with its stability. An added or removed line starting with "frozen"
# changes the Public contract (ADR-0010, ADR-0042): a renamed or new flag, a
# changed default, a new or removed leaf, an exit code. Such a PR must carry
# either a breaking marker in its title (`feat!:`, `fix(scope)!:`, which
# release-please reads as a major bump) or a reference to the ADR that decides
# the change (`ADR-NNNN` in the body). Experimental lines never trip it.
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
frozen=$(printf '%s\n' "$diff" | grep -E '^[+-]frozen ' || true)
if [ -z "$frozen" ]; then
	echo "contract-gate: OK, no frozen line of $golden changed."
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

echo "::error title=Frozen contract changed::this PR changes the frozen command surface (lines below). Mark it breaking with \"!\" in the PR title (feat!: / fix!:), or reference the ADR that decides the change (ADR-NNNN) in the PR body. A NEW leaf not ready to freeze can ship [experimental] instead (kernel.Experimental)." >&2
echo "Changed frozen lines of $golden:" >&2
printf '%s\n' "$frozen" >&2
exit 1
