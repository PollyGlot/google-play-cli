#!/usr/bin/env bash
set -euo pipefail

tag=${1:?usage: release-contributors.sh <tag> [--apply]}
apply=${2:-}
: "${REPO:?REPO is not set}"

start='<!-- contributors:start -->'
end='<!-- contributors:end -->'

prev=$(gh api "repos/$REPO/releases" --paginate \
  --jq '.[] | select(.draft == false and .prerelease == false) | .tag_name' \
  | sort -V | awk -v t="$tag" '$0 == t { print p; exit } { p = $0 }')

args=(-f "tag_name=$tag")
[ -n "$prev" ] && args+=(-f "previous_tag_name=$prev")
notes=$(gh api -X POST "repos/$REPO/releases/generate-notes" "${args[@]}" --jq .body)

prs=$(printf '%s\n' "$notes" | awk '
  /^\* / && match($0, / by @[A-Za-z0-9-]+ in https:\/\/[^ ]*\/pull\/[0-9]+$/) {
    split(substr($0, RSTART + 5), f, " in ")
    print f[1] "\t" f[2]
  }')

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
body=$(printf '%s\n' "$body" | awk -v s="$start" -v e="$end" '
  $0 == s { skip = 1; next }
  $0 == e { skip = 0; next }
  !skip')
body=$(printf '%s' "$body" | sed -e :a -e '/^\n*$/{$d;N;ba' -e '}')
authors=$(printf '%s\n' "$prs" | awk -F'\t' 'NF == 2 { n = $2; sub(/.*\//, "", n); printf "%s=%s ", n, $1 }')
for num in $(printf '%s\n' "$body" | grep '^\* ' | grep -o '(\[#[0-9]*\](' | tr -dc '0-9\n' | sort -u); do
  case " $authors" in *" $num="*) continue ;; esac
  login=$(gh api "repos/$REPO/pulls/$num" --jq 'select(.merged_at != null and .user.type == "User") | .user.login' 2>/dev/null || true)
  [ -n "$login" ] && authors+="$num=$login "
done
body=$(printf '%s\n' "$body" | awk -v map="$authors" '
  BEGIN { n = split(map, kv, " "); for (i = 1; i <= n; i++) { split(kv[i], p, "="); who[p[1]] = p[2] } }
  # A line may cite its issue before its PR ("([#332](..)) ([#350](..))"):
  # the first number that is a PR of this release wins.
  /^\* / && !/ by @[A-Za-z0-9-]+$/ {
    rest = $0; by = ""
    while (by == "" && match(rest, /\(\[#[0-9]+\]\(/)) {
      num = substr(rest, RSTART + 3, RLENGTH - 5)
      if (num in who) by = who[num]
      rest = substr(rest, RSTART + RLENGTH)
    }
    if (by != "") $0 = $0 " by @" by
  }
  { print }')
new_body=$(printf '%s\n\n%s\n' "$body" "$section")
gh api -X PATCH "repos/$REPO/releases/$id" -f body="$new_body" --silent
echo "Contributors section written to $tag."
