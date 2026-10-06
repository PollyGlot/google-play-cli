#!/usr/bin/env bash
# Gate of the slice implementation routine (PRD #501, slice #507), called by
# .github/workflows/slice-agent.yml before it wakes the routine. It answers one
# question from the live state of the tracker, never from the event payload
# (the payload is a snapshot taken when the label landed, and a replay has
# none): may the routine be handed this issue now?
#
# Usage: slice-agent-gate.sh <issue>
#
# Prints key=value lines meant for $GITHUB_OUTPUT:
#   decision=fire          open issue, labelled type:slice and ready-for-agent,
#                          and no open PR refers to it
#   decision=skip-pr       same, but an open PR (draft included) already refers
#                          to it; prs= lists them ("#12 #34")
#   decision=skip-ineligible
#                          closed, or missing one of the two labels; reason=
#                          says which
# Exits non-zero only when gh itself fails. GH_TOKEN must be in the environment.
set -euo pipefail

usage='usage: slice-agent-gate.sh <issue>'
ISSUE="${1:?$usage}"
case "$ISSUE" in
  '' | *[!0-9]*)
    echo "$usage (got '$ISSUE', not an issue number)" >&2
    exit 2
    ;;
esac

issue=$(gh issue view "$ISSUE" --json state,labels)
state=$(jq -r .state <<<"$issue")
has_label() {
  jq -e --arg l "$1" 'any(.labels[].name; . == $l)' <<<"$issue" >/dev/null
}

if [ "$state" != "OPEN" ]; then
  echo "decision=skip-ineligible"
  echo "reason=issue #$ISSUE is $state, not open"
  exit 0
fi
for label in type:slice ready-for-agent; do
  if ! has_label "$label"; then
    echo "decision=skip-ineligible"
    echo "reason=issue #$ISSUE does not carry \`$label\`"
    exit 0
  fi
done

# "Refers to" is deliberately wider than "closes": a PR that only says
# "Part of #N" is still someone's work in flight on that slice, and a second
# branch racing it is exactly what the skip exists to prevent. Four signals:
#   - GitHub's own closing link (Closes/Fixes/Resolves, or a manual link);
#   - #N or owner/repo#N (this repository only) in the title or body, not part
#     of a longer number, and not another repository's #N;
#   - this repository's issue URL ending in /issues/N;
#   - a head branch named after the slice (the routine's own claude/slice-N-...).
# A PR from a fork counts too: the skip only ever costs a comment, while a
# duplicate implementation costs a review.
repo=$(gh repo view --json nameWithOwner --jq .nameWithOwner)
prs=$(gh pr list --state open --limit 300 \
  --json number,title,body,headRefName,closingIssuesReferences \
  | jq -r --argjson n "$ISSUE" --arg repo "$repo" '
      ($n | tostring) as $s
      | ($repo | gsub("(?<c>[.])"; "\\\(.c)")) as $r
      | ("(^|[^0-9A-Za-z_&/])#" + $s + "($|[^0-9])") as $bare
      | ("(^|[^0-9A-Za-z_.-])" + $r + "#" + $s + "($|[^0-9])") as $qualified
      | ($r + "/issues/" + $s + "($|[^0-9])") as $link
      | ("(^|/)(slice-)?" + $s + "($|-)") as $branch
      | .[]
      | select(
          any(.closingIssuesReferences[]?; .number == $n)
          or ((.title + "\n" + (.body // ""))
              | test($bare) or test($qualified; "i") or test($link; "i"))
          or (.headRefName | test($branch))
        )
      | "#\(.number)"' \
  | paste -sd ' ' -)

if [ -n "$prs" ]; then
  echo "decision=skip-pr"
  echo "prs=$prs"
  echo "reason=an open PR already refers to issue #$ISSUE ($prs)"
  exit 0
fi
echo "decision=fire"
