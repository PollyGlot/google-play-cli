#!/usr/bin/env bash
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

list_files() {
	git ls-files |
		grep -vE '^(docs/adr/|CHANGELOG\.md$|vendor/|scripts/verb-gate\.sh$|internal/kernel/removed\.go$|cmd/gplay/removed_test\.go$)|(^|/)migration/'
}

grep_all() {
	list_files | tr '\n' '\0' | xargs -0 grep -nH "$@" 2>/dev/null || true
}

fail=0
report() {
	if [ -n "$2" ]; then
		echo "verb-gate: forbidden pre-rename name: $1" >&2
		printf '%s\n' "$2" >&2
		fail=1
	fi
}

report "apps info -> apps view" \
	"$(grep_all -E '(^|[^[:alnum:]_])apps[[:space:]-]+info([^[:alnum:]_]|$)')"
report "tracks status -> tracks view" \
	"$(grep_all -E '(^|[^[:alnum:]_])tracks[[:space:]-]+status([^[:alnum:]_]|$)')"
report "team grants revoke -> team grants remove" \
	"$(grep_all -E '(^|[^[:alnum:]_])team[[:space:]-]+grants[[:space:]-]+revoke([^[:alnum:]_]|$)')"

report "gplay apps details (read must be 'apps details view')" \
	"$(grep_all -oE 'gplay[[:space:]]+apps[[:space:]]+details([[:space:]]+[a-z-]+)?' | grep -vE 'gplay[[:space:]]+apps[[:space:]]+details[[:space:]]+(view|set)$' || true)"
report "gplay tracks availability (read must be 'tracks availability view')" \
	"$(grep_all -oE 'gplay[[:space:]]+tracks[[:space:]]+availability([[:space:]]+[a-z-]+)?' | grep -vE 'gplay[[:space:]]+tracks[[:space:]]+availability[[:space:]]+view$' || true)"

report "games achievements|leaderboards update|delete -> set|remove" \
	"$(grep_all -E '(^|[^[:alnum:]_])games[[:space:]-]+(achievements|leaderboards)[[:space:]-]+(update|delete)([^[:alnum:]_]|$)')"
report "appstore update -> appstore submit" \
	"$(grep_all -E '(^|[^[:alnum:]_])appstore[[:space:]-]+update([^[:alnum:]_]|$)')"
report "appstore upload apk|image|policy -> appstore apk|image|policy upload" \
	"$(grep_all -E '(^|[^[:alnum:]_])appstore[[:space:]-]+upload([^[:alnum:]_]|$)')"
report "gplay appstore publish-status <state> -> gplay appstore publish-status set <state>" \
	"$(grep_all -oE 'gplay[[:space:]]+appstore[[:space:]]+publish-status([[:space:]]+[a-z<|-]+)?' | grep -vE 'gplay[[:space:]]+appstore[[:space:]]+publish-status[[:space:]]+set$' || true)"

report "releases rollout --to <fraction> -> --staged <fraction>" \
	"$(grep_all -E 'releases[[:space:]]+rollout[^|]*--to[[:space:]=]+[0-9.]')"
report "--max-results / --from-json / --no-validate -> --page-size / --file / --skip-preflight" \
	"$(grep_all -E -- '--(max-results|from-json|no-validate)([^[:alnum:]_-]|$)')"
report "time windows: --start-time/--end-time, reviews history --from/--to -> --since/--until" \
	"$(grep_all -E -- '--(start-time|end-time)([^[:alnum:]_-]|$)|reviews[[:space:]]+history[^|]*--(from|to)[[:space:]=]+[0-9]')"

if [ "$fail" -ne 0 ]; then
	echo "verb-gate: FAILED, use the canonical verbs (docs/adr/0019-canonical-verb-vocabulary.md)." >&2
	exit 1
fi
echo "verb-gate: OK, no pre-rename verb names found."
