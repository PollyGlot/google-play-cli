#!/usr/bin/env bash
# Move the install-skills pin to a commit of the skills repo, and commit it as a
# `chore` so release-please never cuts a release for the pin alone (ADR-0053).
#
# Refuses a commit that is not on the skills repo's main: the pin must name a
# merged, reviewed tree (ADR-0045 §2). The skill list is read back from that
# commit, so a skill added or removed upstream lands in the pin with it.
#
# Usage:
#   scripts/repin-skills.sh <skills-commit>   # write the pin, test, commit
#
# Exit codes: 0 committed (or already pinned), 1 refused, 2 usage error.
# Requires `gh` (authenticated), `jq`, `git` and `go`.

set -euo pipefail

pin=commands/installskills/skills-pin.json

if [[ $# -ne 1 || ! "$1" =~ ^[0-9a-f]{40}$ ]]; then
  echo "usage: scripts/repin-skills.sh <full 40-character skills commit>" >&2
  exit 2
fi
commit=$1

cd "$(git rev-parse --show-toplevel)"
repo=$(jq -r .repo "$pin")
subdir=$(jq -r .subdir "$pin")

if [[ "$(jq -r .commit "$pin")" == "$commit" ]]; then
  echo "repin-skills: $pin already pins $commit"
  exit 0
fi

status=$(gh api "repos/$repo/compare/$commit...main" --jq .status)
if [[ "$status" != "ahead" && "$status" != "identical" ]]; then
  echo "repin-skills: $commit is not on $repo main (compare status: $status)" >&2
  exit 1
fi

skills=$(gh api "repos/$repo/contents/$subdir?ref=$commit" \
  --jq '[.[] | select(.type == "dir") | .name] | sort')

tmp=$(mktemp)
jq --arg c "$commit" --argjson s "$skills" '.commit = $c | .skills = $s' "$pin" >"$tmp"
mv "$tmp" "$pin"

go test ./commands/installskills/

version=$(gh release view --json tagName --jq .tagName)
git add "$pin"
git commit -m "chore(install-skills): pin the skills for ${version#v}" \
  -m "Moves the pin to $repo@$commit. Typed chore so it rides the next release instead of cutting one (ADR-0053)."
echo "repin-skills: pinned $repo@$commit ($(jq '.skills | length' "$pin") skills)"
