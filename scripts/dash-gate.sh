#!/usr/bin/env bash
# dash-gate.sh: keeps the em dash (U+2014) out of Go source.
#
# The em dash is the single most recognisable marker of machine-written prose,
# and it leaks straight to users: `Short`/`Long` strings become `gplay --help`
# and every page under website/src/content/docs/docs/reference/, error strings
# become stderr. Wording is explicitly outside the Public contract
# (docs/DESIGN.md §7), so there is nothing to preserve here.
#
# Use a comma, a colon, parentheses, or end the sentence.
#
# Scope: tracked *.go, excluding _test.go. Test files are exempt on purpose:
# fixtures simulate user-generated content (a review reply, a store listing)
# where an em dash is legitimate data, not gplay's own prose.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

matches="$(
	git ls-files '*.go' |
		grep -vE '(_test\.go$|^vendor/)' |
		tr '\n' '\0' |
		xargs -0 grep -nH -- '—' 2>/dev/null || true
)"

if [ -n "$matches" ]; then
	count="$(printf '%s\n' "$matches" | wc -l | tr -d ' ')"
	echo "dash-gate: FAILED, $count em dash(es) in Go source." >&2
	printf '%s\n' "$matches" >&2
	echo "dash-gate: replace with a comma, a colon, parentheses, or a full stop." >&2
	exit 1
fi
echo "dash-gate: OK, no em dash in Go source."
