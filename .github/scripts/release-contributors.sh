#!/usr/bin/env bash
# Adds a "Contributors" section to a GitHub Release: everyone whose pull
# request shipped in it, then the people whose first contribution it carries.
# The pull requests come from GitHub's release-notes generator ("* <title> by
# @login in <url>"). Its own "New Contributors" block is not used: it missed
# the first merged PR of an outside contributor (#732 in v2.3.3), so a
# newcomer is instead someone whose earliest merged PR is in this release.
# Bots are dropped (dependabot, the gplay App, release-please), and so is
# Claude: it only ever appears as a Co-authored-by trailer, which the
# generator does not read.
#
# Usage: release-contributors.sh <tag> [--apply]
#
# Without --apply, prints the section on stdout. With --apply, writes it into
# the release body between two HTML comment markers, replacing any earlier
# copy, so a rerun (or a workflow_dispatch replay) never duplicates it.
# REPO (owner/name) and GH_TOKEN must be in the environment.
set -euo pipefail

tag=${1:?usage: release-contributors.sh <tag> [--apply]}
apply=${2:-}
: "${REPO:?REPO is not set}"

start='<!-- contributors:start -->'
end='<!-- contributors:end -->'

# The previous non-draft, non-prerelease release in semver order. Asked for
# explicitly: left to itself, the generator picks the latest release older
# than the tag by date, which a backfill of old releases gets wrong.
prev=$(gh api "repos/$REPO/releases" --paginate \
  --jq '.[] | select(.draft == false and .prerelease == false) | .tag_name' \
  | sort -V | awk -v t="$tag" '$0 == t { print p; exit } { p = $0 }')

args=(-f "tag_name=$tag")
[ -n "$prev" ] && args+=(-f "previous_tag_name=$prev")
notes=$(gh api -X POST "repos/$REPO/releases/generate-notes" "${args[@]}" --jq .body)

# "* <title> by @login in <url>" lines as "login<TAB>url", bots dropped.
prs=$(printf '%s\n' "$notes" | awk '
  /^\* / && match($0, / by @[A-Za-z0-9-]+ in https:\/\/[^ ]*\/pull\/[0-9]+$/) {
    split(substr($0, RSTART + 5), f, " in ")
    print f[1] "\t" f[2]
  }')

# Most frequent author first, then by name.
contributors=$(printf '%s\n' "$prs" | cut -f1 | grep . | sort | uniq -c \
  | sort -k1,1nr -k2,2 | awk '{ print "@" $2 }' | paste -sd, - | sed 's/,/, /g' || true)

newcomers=""
for login in $(printf '%s\n' "$prs" | cut -f1 | grep . | sort -u || true); do
  first=$(gh search prs --repo "$REPO" --author "$login" --merged \
    --sort created --order asc --limit 1 --json number --jq '.[0].number')
  url=$(printf '%s\n' "$prs" | awk -F'\t' -v l="$login" -v n="/pull/$first" \
    '$1 == l && substr($2, length($2) - length(n) + 1) == n { print $2; exit }')
  if [ -n "$url" ]; then
    newcomers+="* @$login made their first contribution in $url"$'\n'
  fi
done
newcomers=${newcomers%$'\n'}

if [ -z "$contributors" ]; then
  echo "No human contributor found for $tag; nothing to add." >&2
  exit 0
fi

section=$(
  printf '%s\n\n## Contributors\n\n%s\n' "$start" "$contributors"
  if [ -n "$newcomers" ]; then
    printf '\n### New contributors\n\n%s\n' "$newcomers"
  fi
  printf '\n%s\n' "$end"
)

if [ "$apply" != "--apply" ]; then
  printf '%s\n' "$section"
  exit 0
fi

body=$(gh api "repos/$REPO/releases/tags/$tag" --jq .body)
id=$(gh api "repos/$REPO/releases/tags/$tag" --jq .id)
# Drop an earlier copy of the section, then append the fresh one.
body=$(printf '%s\n' "$body" | awk -v s="$start" -v e="$end" '
  $0 == s { skip = 1; next }
  $0 == e { skip = 0; next }
  !skip')
body=$(printf '%s' "$body" | sed -e :a -e '/^\n*$/{$d;N;ba' -e '}')
# Credit each changelog line to its PR author, as GitHub-generated notes do:
# release-please writes "* **scope:** title ([#763](url)) ([sha](url))", and
# " by @login" goes at the end. A line already credited is left alone.
authors=$(printf '%s\n' "$prs" | awk -F'\t' 'NF == 2 { n = $2; sub(/.*\//, "", n); printf "%s=%s ", n, $1 }')
body=$(printf '%s\n' "$body" | awk -v map="$authors" '
  BEGIN { n = split(map, kv, " "); for (i = 1; i <= n; i++) { split(kv[i], p, "="); who[p[1]] = p[2] } }
  /^\* / && !/ by @[A-Za-z0-9-]+$/ && match($0, /\(\[#[0-9]+\]\(/) {
    num = substr($0, RSTART + 3, RLENGTH - 5)
    if (num in who) $0 = $0 " by @" who[num]
  }
  { print }')
new_body=$(printf '%s\n\n%s\n' "$body" "$section")
gh api -X PATCH "repos/$REPO/releases/$id" -f body="$new_body" --silent
echo "Contributors section written to $tag."
