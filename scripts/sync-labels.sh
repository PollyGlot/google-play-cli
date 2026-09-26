#!/usr/bin/env bash
# Sync GitHub issue/PR labels to the canonical set for google-play-cli.
# Idempotent: existing labels are updated (--force), missing ones are created.
# Labels that exist on GitHub but not here are left alone; --check lists them.
#
# Usage:
#   scripts/sync-labels.sh                  # sync PollyGlot/google-play-cli
#   scripts/sync-labels.sh other/repo       # override repo
#   scripts/sync-labels.sh --check [repo]   # read-only: report drift, exit 1 if any
#
# The list below was regenerated from `gh label list` (2026-09, #603). A label
# created by hand on GitHub belongs here in the same change, or the next
# --check reports it.
#
# Requires the `gh` CLI authenticated with `repo` scope (read-only for --check).

set -euo pipefail

CHECK=0
if [[ "${1:-}" == "--check" ]]; then
  CHECK=1
  shift
fi
REPO="${1:-PollyGlot/google-play-cli}"

if ! command -v gh >/dev/null 2>&1; then
  echo "gh CLI not found. Install: https://cli.github.com/" >&2
  exit 1
fi

# Format: "name|color (no #)|description"
LABELS=(
  # Category: what kind of issue it is (one per issue).
  "bug|d73a4a|Something isn't working as documented"
  "enhancement|a2eeef|New feature, flag, or behavior change"
  "documentation|0075ca|Docs, README, CONTRIBUTING, ADRs"
  "question|d876e3|Usage question, filed through the question issue form"

  # Issue type: planning taxonomy (docs/agents/triage-labels.md).
  "type:prd|0e8a16|Product requirements document: area-level spec, split into type:slice issues"
  "type:slice|1d76db|Tracer-bullet slice of a PRD: vertical end-to-end implementation"
  "type:arch|5319e7|Isolated refactor or architecture decision"
  "type:parking|cccccc|Idea tracked for visibility, deferred out of scope"

  # Area: one per command group or cross-cutting surface.
  "area:auth|1d76db|Authentication, service account, OAuth2"
  "area:apps|0e8a16|apps list / info / registry"
  "area:releases|5319e7|Release uploads, promote, rollout"
  "area:tracks|c2e0c6|Track listing, configuration, custom closed tracks"
  "area:reviews|fbca04|Review listing and replies"
  "area:metadata|006b75|Store listings (per-locale text): edits.listings"
  "area:vitals|d93f0b|Crashes/ANR (Play Developer Reporting API) and ProGuard mappings"
  "area:monetization|1a7f37|Subscriptions v2, one-time IAP, per-territory pricing, RevenueCat sync"
  "area:compliance|5319e7|Data Safety, content rating, Play Console regulatory declarations"
  "area:team|5319e7|Developer account team management: users and grants"
  "area:skills|c5def5|Companion Agent Skills repo (google-play-cli-skills): SKILL.md folders driving gplay"
  "area:schema|a371f7|API schema introspection: Discovery snapshot, gplay schema command, schema-driven tooling"
  "area:platform|1d76db|Cross-cutting CLI platform: kernel, output, transport, config, install, release pipeline"
  "area:device-tiers|0e8a16|Device tier configs: device targeting for tiered delivery (deviceTierConfigs)"
  "area:recovery|5319e7|App recovery: incident-response remediation (apprecovery)"
  "area:customapps|5319e7|Custom apps: managed Google Play private publishing (playcustomapp)"
  "area:games|5319e7|Play Games Services config: achievements and leaderboards (gamesConfiguration)"
  "area:site|c5def5|Public website (landing and docs): Astro/Starlight, served from the install Worker (ADR-0025)"
  "area:appstore|0e8a16|App store hosted apps (alternative distribution / DMA): appstoreappsreview"

  # Priority
  "priority:high|b60205|Drop everything"
  "priority:medium|d93f0b|Next release window"
  "priority:low|fef2c0|Eventually"

  # Triage state and community. `triage` is what the feature request and
  # question forms apply; the bug form applies `needs-triage`.
  "triage|ededed|Filed through the feature request or question form, awaiting triage"
  "needs-triage|ededed|Newly filed, awaiting label review"
  "needs-info|e4ad17|Author follow-up needed before triage can complete"
  "needs-decision|d93f0b|Needs a product, contract or design decision before code"
  "ready-for-agent|0e8a16|PRD or issue is fully specified and can be picked up by an implementing agent"
  "good first issue|7057ff|Approachable for first-time contributors"
  "help wanted|008672|Open for contribution"

  # Resolution
  "wontfix|ffffff|Decision: out of scope"
  "duplicate|cccccc|Closed in favor of another issue/PR"

  # Discovery bot: machine-readable state of the rolling refresh PR.
  # discovery:schema/surface are set by .github/workflows/discovery-watch.yml;
  # verdict-merge / needs-decision are set by the triage routine (#506) and read
  # back by CI to decide whether to merge or to page the maintainer.
  "discovery:schema|a371f7|Discovery refresh: schema details changed, no method surface change"
  "discovery:surface|a371f7|Discovery refresh: methods added or removed (ADR-0026 grilling)"
  "discovery:verdict-merge|0e8a16|Discovery triage cleared the refresh PR for merge"
  "discovery:needs-decision|b60205|Discovery triage needs a product decision from the maintainer"

  # Audit 2026-09: finding category of each audit issue.
  "audit:architecture|5319e7|Audit 2026-09 finding category: architecture"
  "audit:api|0e8a16|Audit 2026-09 finding category: api"
  "audit:performance|fbca04|Audit 2026-09 finding category: performance"
  "audit:coherence|c5def5|Audit 2026-09 finding category: coherence"
  "audit:tests|bfd4f2|Audit 2026-09 finding category: tests"
  "audit:ci|1d76db|Audit 2026-09 finding category: ci"
  "audit:site|f9d0c4|Audit 2026-09 finding category: site"
  "audit:tooling|d4c5f9|Audit 2026-09 finding category: tooling"
  "audit:security|b60205|Audit 2026-09 finding category: security"

  # Lifecycle and release automation. release-please sets the autorelease
  # labels on its release PR; they are listed so --check stays quiet on them.
  "breaking-change|b60205|Backwards-incompatible behavior change"
  "dependencies|0366d6|Updates to Go modules or GitHub Actions"
  "affects:storedeck|8250df|Changes the gplay contract storedeck consumes: mirrored to storedeck on release"
  "autorelease: pending|ededed|"
  "autorelease: tagged|ededed|"
)

if [[ "$CHECK" == 1 ]]; then
  # comm needs both sides sorted under the same collation; LC_ALL=C pins it.
  script_names="$(printf '%s\n' "${LABELS[@]}" | cut -d'|' -f1 | LC_ALL=C sort)"
  live_names="$(gh label list --repo "$REPO" --limit 500 --json name --jq '.[].name' | LC_ALL=C sort)"
  only_live="$(LC_ALL=C comm -13 <(printf '%s\n' "$script_names") <(printf '%s\n' "$live_names"))"
  only_script="$(LC_ALL=C comm -23 <(printf '%s\n' "$script_names") <(printf '%s\n' "$live_names"))"
  if [[ -z "$only_live" && -z "$only_script" ]]; then
    echo "Labels on $REPO match this script."
    exit 0
  fi
  # Line by line: label names carry spaces ("good first issue").
  while IFS= read -r name; do
    if [[ -n "$name" ]]; then echo "  on GitHub, not in this script: $name"; fi
  done <<< "$only_live"
  while IFS= read -r name; do
    if [[ -n "$name" ]]; then echo "  in this script, not on GitHub: $name"; fi
  done <<< "$only_script"
  exit 1
fi

echo "Syncing labels on $REPO..."

for entry in "${LABELS[@]}"; do
  IFS='|' read -r name color description <<< "$entry"
  if gh label create "$name" --repo "$REPO" --color "$color" --description "$description" --force >/dev/null 2>&1; then
    echo "  ✓ $name"
  else
    echo "  ✗ failed: $name" >&2
  fi
done
