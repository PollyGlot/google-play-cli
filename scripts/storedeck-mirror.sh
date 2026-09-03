#!/usr/bin/env bash
# Mirror the contract changes of a gplay release into an issue on PollyGlot/storedeck.
#
# storedeck consumes the `gplay` CLI contract, not the Google API, so it learns
# about changes when a version SHIPS, not when a Discovery snapshot moves
# (PRD #501). A slice that changes a command's contract carries the
# `affects:storedeck` label; this script collects those issues for one version
# and renders (or opens/updates) a single mirror issue in the target repo.
#
# Usage:
#   scripts/storedeck-mirror.sh --tag v1.4.1 [--previous-tag v1.4.0] [--dry-run]
#
# Options:
#   --tag TAG           Released tag to mirror (required).
#   --previous-tag TAG  Tag the release is diffed against. Default: the previous
#                       tag reachable from TAG (`git describe --abbrev=0 TAG^`).
#   --source REPO       gplay repo. Default: PollyGlot/google-play-cli.
#   --target REPO       Mirror repo. Default: PollyGlot/storedeck.
#   --label LABEL       Contract label. Default: affects:storedeck.
#   --dry-run           Print the rendered issue to stdout, write nothing.
#
# Requires: git (full history), gh authenticated. Writing to the target repo
# needs a cross-repo credential — see .github/workflows/storedeck-mirror.yml.
#
# Exit codes: 0 done (including "nothing to mirror"), 1 usage/runtime error.

set -euo pipefail

TAG=""
PREVIOUS_TAG=""
SOURCE_REPO="${GPLAY_SOURCE_REPO:-PollyGlot/google-play-cli}"
TARGET_REPO="${STOREDECK_REPO:-PollyGlot/storedeck}"
LABEL="affects:storedeck"
DRY_RUN=false

die() { echo "storedeck-mirror: $*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --tag)          TAG="${2:?--tag needs a value}"; shift 2 ;;
    --previous-tag) PREVIOUS_TAG="${2:?--previous-tag needs a value}"; shift 2 ;;
    --source)       SOURCE_REPO="${2:?--source needs a value}"; shift 2 ;;
    --target)       TARGET_REPO="${2:?--target needs a value}"; shift 2 ;;
    --label)        LABEL="${2:?--label needs a value}"; shift 2 ;;
    --dry-run)      DRY_RUN=true; shift ;;
    -h|--help)      sed -n '2,30p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *)              die "unknown argument: $1" ;;
  esac
done

[ -n "$TAG" ] || die "missing --tag"
command -v gh >/dev/null 2>&1 || die "gh CLI not found. Install: https://cli.github.com/"

git rev-parse -q --verify "refs/tags/$TAG" >/dev/null 2>&1 \
  || die "tag $TAG not found locally (the workflow checks out with fetch-depth: 0)"

if [ -z "$PREVIOUS_TAG" ]; then
  # `git describe` walks back from the tag's parent, so it lands on the previous
  # release even when tags are not chronologically ordered on the branch.
  PREVIOUS_TAG=$(git describe --tags --abbrev=0 "$TAG^" 2>/dev/null || true)
fi

if [ -n "$PREVIOUS_TAG" ]; then
  RANGE="$PREVIOUS_TAG..$TAG"
else
  # First release ever: everything reachable from the tag is "new".
  RANGE="$TAG"
fi

echo "storedeck-mirror: scanning $RANGE for issues labelled '$LABEL'" >&2

# The repo is squash-merge only, so every commit subject in the range ends with
# the merged PR number: "type(scope): subject (#123)".
PR_NUMBERS=$(git log --no-merges --format='%s' "$RANGE" \
  | sed -n 's/.*(#\([0-9][0-9]*\))$/\1/p' \
  | sort -un || true)

if [ -z "$PR_NUMBERS" ]; then
  echo "storedeck-mirror: no merged PRs in $RANGE — nothing to mirror." >&2
  exit 0
fi

# Ask GitHub which issues each PR closed, and with which labels. Doing it per PR
# keeps the query small and survives a PR that closes nothing.
ROWS=""
for pr in $PR_NUMBERS; do
  rows=$(gh api graphql \
    -F owner="${SOURCE_REPO%%/*}" -F name="${SOURCE_REPO##*/}" -F pr="$pr" \
    -f query='
      query($owner:String!, $name:String!, $pr:Int!) {
        repository(owner:$owner, name:$name) {
          pullRequest(number:$pr) {
            closingIssuesReferences(first:20) {
              nodes {
                number title url
                labels(first:30) { nodes { name } }
              }
            }
          }
        }
      }' \
    --jq '.data.repository.pullRequest.closingIssuesReferences.nodes[]
          | select(any(.labels.nodes[].name; . == "'"$LABEL"'"))
          | [(.number|tostring), .title, .url,
             ([.labels.nodes[].name | select(startswith("area:"))] | join(","))]
          | @tsv' 2>/dev/null || true)
  [ -n "$rows" ] && ROWS="${ROWS}${rows}"$'\n'
done

ROWS=$(printf '%s' "$ROWS" | sed '/^$/d' | sort -t"$(printf '\t')" -k1,1n -u || true)

MARKER="<!-- gplay-storedeck-mirror:$TAG -->"
TITLE="gplay $TAG: contract changes to mirror"
BODY_FILE=$(mktemp)
trap 'rm -f "$BODY_FILE"' EXIT

if [ -z "$ROWS" ]; then
  echo "storedeck-mirror: no issue labelled '$LABEL' in $RANGE — nothing to mirror." >&2
  {
    echo "### storedeck mirror"
    echo
    echo "Nothing to mirror for \`$TAG\`: no closed issue in \`$RANGE\` carries the \`$LABEL\` label."
  } >> "${GITHUB_STEP_SUMMARY:-/dev/null}"
  exit 0
fi

# Render the issue body. Each row is: number, title, url, area labels.
{
  echo "$MARKER"
  echo
  echo "\`gplay\` [\`$TAG\`](https://github.com/$SOURCE_REPO/releases/tag/$TAG) ships changes to the CLI contract that storedeck consumes."
  echo
  echo "## Contract changes"
  echo
  while IFS=$'\t' read -r number title url areas; do
    [ -n "$number" ] || continue
    # The command is named in the slice title when there is one ("`gplay x y`
    # does Z"); otherwise the area labels are the closest honest pointer.
    command=$(printf '%s' "$title" | sed -n 's/.*`\(gplay [^`]*\)`.*/\1/p')
    if [ -n "$command" ]; then
      scope="\`$command\`"
    elif [ -n "$areas" ]; then
      scope="$(printf '%s' "$areas" | sed 's/,/, /g')"
    else
      scope="_unscoped_"
    fi
    echo "- [$SOURCE_REPO#$number]($url) — $title ($scope)"
  done <<< "$ROWS"
  echo
  echo "## What storedeck should do"
  echo
  echo "1. Read each linked issue and the \`--help\` of the command it names."
  echo "2. Adapt storedeck's call sites, or confirm the change is additive and no-op here."
  echo "3. Close this issue once storedeck is aligned with \`$TAG\`."
  echo
  echo "_Opened automatically by the [storedeck mirror workflow](https://github.com/$SOURCE_REPO/blob/main/.github/workflows/storedeck-mirror.yml) in \`$SOURCE_REPO\`. Re-running it for \`$TAG\` updates this issue in place._"
} > "$BODY_FILE"

if [ "$DRY_RUN" = true ]; then
  echo "storedeck-mirror: dry run — would open/update in $TARGET_REPO:" >&2
  echo "--- title ---"
  echo "$TITLE"
  echo "--- body ---"
  cat "$BODY_FILE"
  exit 0
fi

# Idempotency: the marker (not the title) identifies the mirror issue, so a title
# reword never produces a duplicate. Search covers closed issues too.
EXISTING=$(gh search issues "$MARKER" --repo "$TARGET_REPO" --json number --jq '.[0].number' 2>/dev/null || true)
if [ -z "$EXISTING" ]; then
  EXISTING=$(gh issue list --repo "$TARGET_REPO" --state all --limit 200 \
    --search "in:body $TAG" --json number,body \
    --jq 'map(select(.body | contains("'"$MARKER"'"))) | .[0].number' 2>/dev/null || true)
fi

if [ -n "$EXISTING" ] && [ "$EXISTING" != "null" ]; then
  gh issue edit "$EXISTING" --repo "$TARGET_REPO" --title "$TITLE" --body-file "$BODY_FILE"
  ISSUE_URL="https://github.com/$TARGET_REPO/issues/$EXISTING"
  ACTION="Updated"
else
  ISSUE_URL=$(gh issue create --repo "$TARGET_REPO" --title "$TITLE" --body-file "$BODY_FILE")
  ACTION="Opened"
fi

echo "storedeck-mirror: $ACTION $ISSUE_URL" >&2
{
  echo "### storedeck mirror"
  echo
  echo "$ACTION $ISSUE_URL for \`$TAG\` ($(printf '%s\n' "$ROWS" | wc -l | tr -d ' ') contract change(s))."
} >> "${GITHUB_STEP_SUMMARY:-/dev/null}"
