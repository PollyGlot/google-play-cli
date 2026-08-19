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
# Legitimate historical mentions are excluded: docs/adr/** (ADRs are
# point-in-time records; ADR-0019 holds the rename table) and CHANGELOG.md.
#
# For the two group renames: a `gplay <group>` example must always carry a
# verb, so only the `gplay `-prefixed form is checked. Prose may still refer
# to the bare group noun without the prefix (e.g. "the `apps details` group").
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

# Tracked files minus historical records, the vendor tree, and this script.
list_files() {
	git ls-files |
		grep -vE '^(docs/adr/|CHANGELOG\.md$|vendor/|scripts/verb-gate\.sh$)'
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

if [ "$fail" -ne 0 ]; then
	echo "verb-gate: FAILED, use the canonical verbs (docs/adr/0019-canonical-verb-vocabulary.md)." >&2
	exit 1
fi
echo "verb-gate: OK, no pre-rename verb names found."
