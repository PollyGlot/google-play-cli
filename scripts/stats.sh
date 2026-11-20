#!/usr/bin/env bash

set -euo pipefail

REPO="${1:-PollyGlot/google-play-cli}"

if ! command -v gh >/dev/null 2>&1; then
  echo "gh CLI not found. Install: https://cli.github.com/" >&2
  exit 1
fi

tsv="$(
  gh api --paginate "repos/$REPO/releases" --jq '
    .[]
    | {
        tag:  .tag_name,
        date: ((.published_at // "")[0:10]),
        bins: [ .assets[] | select(.name | test("\\.(tar\\.gz|zip)$")) ],
        all:  ([ .assets[].download_count ] | add // 0)
      }
    | .nbin   = (.bins | length)
    | .binsum = ([ .bins[].download_count ] | add // 0)
    | .hit    = ([ .bins[] | select(.download_count > 0) ] | length)
    | .uniq   = ([ .bins[].download_count ] | unique | length)
    | .flag   = (if (.nbin > 1 and .hit == .nbin and .uniq == 1 and .binsum > 0)
                 then "BOT" else "" end)
    | [ .tag, .date, (.binsum|tostring), (.all|tostring), .flag ] | @tsv
  '
)"

if [ -z "$tsv" ]; then
  echo "No releases found for $REPO." >&2
  exit 0
fi

read -r stars <<< "$(gh repo view "$REPO" --json stargazerCount --jq '.stargazerCount' 2>/dev/null || echo "?")"
latest="$(gh release view --repo "$REPO" --json tagName --jq '.tagName' 2>/dev/null || echo "")"
today="$(date +%F)"

printf 'gplay — download stats (GitHub Releases)        %s\n' "$today"
printf 'repo: %s   stars: %s\n\n' "$REPO" "$stars"

awk -F'\t' -v latest="$latest" '
BEGIN {
  printf "%-16s %-12s %9s %10s  %s\n", "Release", "Date", "Binaries", "AllAssets", "Note"
  printf "%s\n", "-------------------------------------------------------------------------"
}
{
  tag=$1; date=$2; bin=$3+0; all=$4+0; flag=$5
  note=""
  if (tag==latest)  note="<- latest stable"
  if (flag=="BOT")  note=note (note?"  " :"") "(uniform across all archives -> likely bot/mirror)"
  printf "%-16s %-12s %9d %10d  %s\n", tag, date, bin, all, note
  sumbin+=bin; sumall+=all; n++
  if (flag!="BOT") human+=bin; else bot+=bin
}
END {
  printf "%s\n", "-------------------------------------------------------------------------"
  printf "%-40s %6d\n", "Binaries (install script + brew + manual):", sumbin
  printf "%-40s %6d\n", "  - excl. suspected bot/mirror:",            human
  printf "%-40s %6d\n", "All assets (incl. checksums/sig/SBOM):",    sumall
  printf "%-40s %6d\n", "Releases published:",                       n
  print ""
  print "Note: GitHub merges brew + install-script + manual into one count and"
  print "does not exclude bots. \"curl gplay.sh/install\" fetches are tracked"
  print "separately in Cloudflare Worker Logs, not here."
}
' <<< "$tsv"
