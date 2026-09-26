#!/usr/bin/env bash
# Squash-merge a pull request as the solo maintainer, but only when it is safe.
#
# The main ruleset requires an approving review and up-to-date required checks,
# and the repo has a single maintainer who cannot approve their own PR, so every
# merge goes through `gh pr merge --admin`. --admin also bypasses the
# "branch must be up to date" rule, which is how two PRs regenerating
# docs/COVERAGE.md merged 25 s apart and left main red (#547/#548). This script
# puts that rule back in front of the admin merge. It refuses when:
#
#   - the PR head does not contain the current HEAD of origin/<base>, or
#   - a check the base branch requires is missing, pending, or not green.
#
# Otherwise it runs `gh pr merge <n> --admin --squash --match-head-commit <sha>`.
# --match-head-commit closes the race where someone pushes to the PR between the
# checks being read and the merge: GitHub then rejects the merge instead of
# squashing a head nobody verified.
#
# Usage:
#   scripts/merge-pr.sh <number>             # verify, then merge
#   scripts/merge-pr.sh --dry-run <number>   # verify only, never merges
#
# Exit codes: 0 merged (or dry run passed), 1 refused, 2 usage error.
# Requires `gh` (authenticated) and `git` with an `origin` remote on GitHub.

set -euo pipefail

usage() {
  cat >&2 <<'EOF'
usage: scripts/merge-pr.sh [--dry-run] <number>
  --dry-run   run every gate, print the merge command, never merge
EOF
  exit 2
}

refuse() {
  echo "refused: $*" >&2
  exit 1
}

dry_run=false
pr=""
for arg in "$@"; do
  case "$arg" in
    --dry-run) dry_run=true ;;
    -h|--help) usage ;;
    -*) echo "unknown flag: $arg" >&2; usage ;;
    *)
      [ -z "$pr" ] || usage
      pr="$arg"
      ;;
  esac
done
case "$pr" in
  ''|*[!0-9]*) usage ;;
esac

for tool in gh git; do
  command -v "$tool" >/dev/null 2>&1 || { echo "$tool not found" >&2; exit 2; }
done

# Run git and gh from the repository this script belongs to, whatever the cwd:
# gh resolves {owner}/{repo} from the git remote.
cd "$(git -C "$(dirname "$0")" rev-parse --show-toplevel)"

remote=origin

# One tab-separated line; the title comes last because it may contain spaces.
IFS=$'\t' read -r state draft base head_oid head_ref title < <(
  gh pr view "$pr" --json state,isDraft,baseRefName,headRefOid,headRefName,title \
    --jq '[.state, (.isDraft|tostring), .baseRefName, .headRefOid, .headRefName, .title] | @tsv'
) || refuse "could not read PR #$pr (gh pr view failed)"

echo "PR #$pr: $title"
echo "  head $head_ref @ ${head_oid:0:12}, base $base"

[ "$state" = OPEN ] || refuse "PR #$pr is $state, not OPEN"

# --- 1. The head must contain the current base HEAD --------------------------
# Fetch both sides fresh: a stale local origin/<base> would let a behind branch
# through, which is the exact failure this script exists to stop.
git fetch --quiet "$remote" "+refs/heads/$base:refs/remotes/$remote/$base"
git fetch --quiet "$remote" "refs/pull/$pr/head"
fetched_head=$(git rev-parse FETCH_HEAD)
[ "$fetched_head" = "$head_oid" ] ||
  refuse "PR #$pr head moved while checking (${head_oid:0:12} -> ${fetched_head:0:12}); rerun"
base_oid=$(git rev-parse "refs/remotes/$remote/$base")

if ! git merge-base --is-ancestor "$base_oid" "$head_oid"; then
  behind=$(git rev-list --count "$head_oid..$base_oid")
  cat >&2 <<EOF
refused: PR #$pr is $behind commit(s) behind $remote/$base.
  Its head ${head_oid:0:12} does not contain $base ${base_oid:0:12}, so its green
  checks say nothing about the squash that would land. Bring the branch up to
  date with a merge (not a rebase: the repo squash-merges, so a merge commit on
  the branch costs nothing and needs no force-push), then rerun once the
  required checks are green on the new head:

    git switch $head_ref            # in the branch's own worktree
    git fetch $remote && git merge $remote/$base
    git push
    gh pr checks $pr --required --watch
    scripts/merge-pr.sh $pr

  (\`gh pr update-branch $pr\` does the same merge server-side.)
EOF
  exit 1
fi
echo "  up to date: head contains $remote/$base @ ${base_oid:0:12}"

# --- 2. Every check the base branch requires must be green -------------------
# The required contexts come from the branch rules (ruleset or classic
# protection), not from a list kept here that could drift from GitHub.
required=$(gh api "repos/{owner}/{repo}/rules/branches/$base" \
  --jq '.[] | select(.type == "required_status_checks") | .parameters.required_status_checks[].context' |
  sort -u) || refuse "could not read the required checks of $base"
[ -n "$required" ] ||
  refuse "no required status checks found on $base; refusing rather than merging unchecked"

# gh exits non-zero while checks fail (1) or are pending (8) even with --json,
# so the exit code is ignored and the JSON read instead. An empty answer (gh
# failed outright) leaves every required check missing, which refuses.
checks=$(gh pr checks "$pr" --json name,bucket --jq '.[] | [.name, .bucket] | @tsv' 2>/dev/null || true)

not_green=""
while IFS= read -r ctx; do
  buckets=$(printf '%s\n' "$checks" | awk -F'\t' -v n="$ctx" '$1 == n { print $2 }' | sort -u)
  if [ -z "$buckets" ]; then
    not_green="$not_green\n    $ctx: not reported"
  elif [ "$buckets" != pass ]; then
    not_green="$not_green\n    $ctx: $(printf '%s' "$buckets" | tr '\n' ',' | sed 's/,$//')"
  else
    echo "  required check green: $ctx"
  fi
done <<<"$required"

if [ -n "$not_green" ]; then
  printf 'refused: PR #%s has required checks that are not green:%b\n' "$pr" "$not_green" >&2
  cat >&2 <<EOF
  Wait for them (gh pr checks $pr --required --watch), fix any red one on the
  branch, then rerun scripts/merge-pr.sh $pr.
EOF
  exit 1
fi

# --- 3. Merge ----------------------------------------------------------------
# Checked last: it is the cheapest gate to clear, so the ones above report first.
[ "$draft" = false ] || refuse "PR #$pr is a draft; mark it ready first (gh pr ready $pr)"

merge_cmd=(gh pr merge "$pr" --admin --squash --match-head-commit "$head_oid")

if [ "$dry_run" = true ]; then
  echo "dry run: all gates pass; would run: ${merge_cmd[*]}"
  exit 0
fi

# Last look at the base: a merge that landed while the checks were being read
# would make this PR behind again. The remaining window is seconds wide.
live_base=$(git ls-remote "$remote" "refs/heads/$base" | cut -f1)
[ "$live_base" = "$base_oid" ] ||
  refuse "$base moved to ${live_base:0:12} while checking; rerun to re-verify"

"${merge_cmd[@]}"
