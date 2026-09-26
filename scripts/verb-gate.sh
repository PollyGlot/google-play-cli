#!/usr/bin/env bash
# verb-gate.sh: guards the ADR-0019 canonical verb vocabulary (#98 / #168).
#
# Fails if a pre-rename command name reappears anywhere in tracked source or
# docs. The renames (docs/adr/0019-canonical-verb-vocabulary.md):
#
#   apps info             -> apps view
#   tracks status         -> tracks view
#   team grants revoke    -> team grants remove
#   apps details (read)   -> apps details view        (bare = group/help only)
#   tracks availability   -> tracks availability view  (bare = group/help only)
#
# And the 2.0.0 renames (#597, ADR-0048), no alias kept:
#
#   games achievements|leaderboards update|delete -> ... set|remove
#   appstore update                     -> appstore submit
#   appstore upload apk|image|policy    -> appstore apk|image|policy upload
#   appstore publish-status <state>     -> appstore publish-status set <state>
#   releases rollout --to <fraction>    -> releases rollout --staged <fraction>
#   --max-results / --from-json / --no-validate -> --page-size / --file / --skip-preflight
#   --start-time/--end-time, reviews history --from/--to -> --since/--until
#
# (The other 2.0.0 flag renames, `--version` -> `--version-code` on vitals,
# `--region` -> `--regions`, `--kind` -> `--format`, are too common as words
# to grep; the surface golden and TestLeafContract hold them.)
#
# Legitimate historical mentions are excluded: docs/adr/** (ADRs are
# point-in-time records; ADR-0019 holds the rename table), CHANGELOG.md, and
# migration guides (any path with a `migration/` directory), whose job is to
# name the old spelling next to the new one.
#
# For the two group renames: a `gplay <group>` example must always carry a
# verb, so only the `gplay `-prefixed form is checked. Prose may still refer
# to the bare group noun without the prefix (e.g. "the `apps details` group").
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

# Tracked files minus historical records, the vendor tree, and this script.
list_files() {
	git ls-files |
		grep -vE '^(docs/adr/|CHANGELOG\.md$|vendor/|scripts/verb-gate\.sh$)|(^|/)migration/'
}

# grep_all <extra-grep-args...> <pattern>: grep the file set, always
# prefixing the filename, never failing the script when there is no match.
grep_all() {
	list_files | tr '\n' '\0' | xargs -0 grep -nH "$@" 2>/dev/null || true
}

fail=0
report() { # <label> <matches>
	if [ -n "$2" ]; then
		echo "verb-gate: forbidden pre-rename name: $1" >&2
		printf '%s\n' "$2" >&2
		fail=1
	fi
}

# 1–3: fully-removed command names (no legitimate use outside the exclusions).
# Patterns use explicit non-word boundaries ([^[:alnum:]_] / ^ / $) and a
# flexible separator [[:space:]-]+ that matches BOTH the command form ("apps
# info") and the hyphenated adjective form ("apps-info envelope") in prose, so
# neither slips through. POSIX classes only, portable GNU/BSD.
report "apps info -> apps view" \
	"$(grep_all -E '(^|[^[:alnum:]_])apps[[:space:]-]+info([^[:alnum:]_]|$)')"
report "tracks status -> tracks view" \
	"$(grep_all -E '(^|[^[:alnum:]_])tracks[[:space:]-]+status([^[:alnum:]_]|$)')"
report "team grants revoke -> team grants remove" \
	"$(grep_all -E '(^|[^[:alnum:]_])team[[:space:]-]+grants[[:space:]-]+revoke([^[:alnum:]_]|$)')"

# 4–5: bare-read regressions. A `gplay <group>` invocation must carry a verb;
# the read is `... view` (and, for apps details, the write is `... set`).
report "gplay apps details (read must be 'apps details view')" \
	"$(grep_all -oE 'gplay[[:space:]]+apps[[:space:]]+details([[:space:]]+[a-z-]+)?' | grep -vE 'gplay[[:space:]]+apps[[:space:]]+details[[:space:]]+(view|set)$' || true)"
report "gplay tracks availability (read must be 'tracks availability view')" \
	"$(grep_all -oE 'gplay[[:space:]]+tracks[[:space:]]+availability([[:space:]]+[a-z-]+)?' | grep -vE 'gplay[[:space:]]+tracks[[:space:]]+availability[[:space:]]+view$' || true)"

# 6–9: the 2.0.0 verb renames (#597). Same boundary and separator rules as 1–3.
report "games achievements|leaderboards update|delete -> set|remove" \
	"$(grep_all -E '(^|[^[:alnum:]_])games[[:space:]-]+(achievements|leaderboards)[[:space:]-]+(update|delete)([^[:alnum:]_]|$)')"
report "appstore update -> appstore submit" \
	"$(grep_all -E '(^|[^[:alnum:]_])appstore[[:space:]-]+update([^[:alnum:]_]|$)')"
report "appstore upload apk|image|policy -> appstore apk|image|policy upload" \
	"$(grep_all -E '(^|[^[:alnum:]_])appstore[[:space:]-]+upload([^[:alnum:]_]|$)')"
report "gplay appstore publish-status <state> -> gplay appstore publish-status set <state>" \
	"$(grep_all -oE 'gplay[[:space:]]+appstore[[:space:]]+publish-status([[:space:]]+[a-z<|-]+)?' | grep -vE 'gplay[[:space:]]+appstore[[:space:]]+publish-status[[:space:]]+set$' || true)"

# 10–11: the 2.0.0 flag renames that are distinctive enough to grep.
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
