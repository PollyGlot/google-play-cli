#!/usr/bin/env bash
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
