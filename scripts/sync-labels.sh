#!/usr/bin/env bash
# Sync GitHub issue/PR labels to the canonical set for google-play-cli.
# Idempotent: existing labels are updated (--force), missing ones are created.
#
# Usage:
#   scripts/sync-labels.sh                  # default: PollyGlot/google-play-cli
#   scripts/sync-labels.sh other/repo       # override repo
#
# Requires the `gh` CLI authenticated with `repo` scope.

set -euo pipefail

REPO="${1:-PollyGlot/google-play-cli}"

if ! command -v gh >/dev/null 2>&1; then
  echo "gh CLI not found. Install: https://cli.github.com/" >&2
  exit 1
fi

# Format: "name|color (no #)|description"
LABELS=(
  # Type
  "bug|d73a4a|Something isn't working as documented"
  "enhancement|a2eeef|New feature, flag, or behavior change"
  "documentation|0075ca|Docs, README, CONTRIBUTING, ADRs"
  "question|d876e3|Routed from Issues to Discussions"

  # Area — one per command group
  "area:auth|1d76db|Authentication, service account, OAuth2"
  "area:apps|0e8a16|apps list / info / registry"
  "area:releases|5319e7|Release uploads, promote, rollout"
  "area:tracks|c2e0c6|Track listing, configuration, custom closed tracks"
  "area:reviews|fbca04|Review listing and replies"

  # Priority
  "priority:high|b60205|Drop everything"
  "priority:medium|d93f0b|Next release window"
  "priority:low|fef2c0|Eventually"

  # Triage & community
  "good first issue|7057ff|Approachable for first-time contributors"
  "help wanted|008672|Open for contribution"
  "needs-info|e4ad17|Author follow-up needed before triage can complete"
  "needs-triage|ededed|Newly filed, awaiting label review"

  # Resolution
  "wontfix|ffffff|Decision: out of scope"
  "duplicate|cccccc|Closed in favor of another issue/PR"

  # Downstream — consumed by the release-time mirror workflow
  # (.github/workflows/storedeck-mirror.yml, PRD #501)
  "affects:storedeck|8250df|Changes the gplay contract storedeck consumes — mirrored to storedeck on release"
  # Discovery bot — machine-readable state of the rolling refresh PR.
  # discovery:schema/surface are set by .github/workflows/discovery-watch.yml;
  # verdict-merge / needs-decision are set by the triage routine (#506) and read
  # back by CI to decide whether to merge or to page the maintainer.
  "discovery:schema|a371f7|Discovery refresh: schema details changed, no method surface change"
  "discovery:surface|a371f7|Discovery refresh: methods added or removed (ADR-0026 grilling)"
  "discovery:verdict-merge|0e8a16|Discovery triage cleared the refresh PR for merge"
  "discovery:needs-decision|b60205|Discovery triage needs a product decision from the maintainer"

  # Lifecycle
  "breaking-change|b60205|Backwards-incompatible behavior change"
  "dependencies|0366d6|Updates to Go modules or GitHub Actions"
)

echo "Syncing labels on $REPO..."

for entry in "${LABELS[@]}"; do
  IFS='|' read -r name color description <<< "$entry"
  if gh label create "$name" --repo "$REPO" --color "$color" --description "$description" --force >/dev/null 2>&1; then
    echo "  ✓ $name"
  else
    echo "  ✗ failed: $name" >&2
  fi
done

echo
echo "Done. Optional cleanup (default labels you may not want):"
echo "  gh label delete invalid       --repo $REPO --yes"
echo "  gh label delete 'help wanted' --repo $REPO --yes   # only if duplicate exists"
