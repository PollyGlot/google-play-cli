#!/usr/bin/env bash
#
# Fallback for gplay-release-announce step 6 when the Typefully MCP is NOT
# connected. The MCP path in SKILL.md is preferred; this exists only so the
# upload+schedule still works from a plain shell.
#
# Order matches SKILL.md: upload both PNGs FIRST, then create the scheduled
# draft with the media_ids already inlined — so a scheduled draft is never
# left imageless. Slot picking is interactive in the skill (AskUserQuestion);
# here the slot is just an argument.
#
# Endpoints/auth: Typefully REST **v2** with **Authorization: Bearer** (verified
# empirically 2026-07-08 against social_set 312931 / @PollyGlot — v1 paths 404,
# X-API-KEY → 403). Captured in
# ~/Documents/gplay-marketing/skill-additions-claude-code.md. If a call 4xx's,
# confirm the path/header against Typefully's current API reference — the live
# truth is the MCP tool set, which this only shadows.
#
# Usage:
#   TYPEFULLY_API_KEY=… typefully_publish.sh \
#       v0.10.0 \
#       2026-06-30T17:00:00Z \                 # slot 'at' (UTC) or: next-free-slot
#       msg1.txt msg2.txt \                    # the two message bodies
#       release.png terminal.png               # msg1 image, msg2 image
set -euo pipefail
: "${TYPEFULLY_API_KEY:?export TYPEFULLY_API_KEY first}"

SSID="${SSID:-312931}"                          # @PollyGlot
API="${TYPEFULLY_API:-https://api.typefully.com/v2}"
AUTH=(-H "Authorization: Bearer $TYPEFULLY_API_KEY")

VER="$1"; SLOT="$2"
MSG1="$(cat "$3")"; MSG2="$(cat "$4")"
PNG1="$5"; PNG2="$6"

# Length guard (X 280). Threads 500 is laxer, so 280 is the binding bound.
i=0
for m in "$MSG1" "$MSG2"; do
  i=$((i + 1))
  len=$(printf '%s' "$m" | wc -m | tr -d ' ')
  [ "$len" -le 280 ] || { echo "msg$i too long: $len > 280" >&2; exit 1; }
done

# 1) Upload one PNG with a raw PUT, echo its media_id.
#    The presigned URL is signed WITHOUT headers: add none, or S3 returns
#    403 SignatureDoesNotMatch. curl -T sends raw bytes (not --data-binary).
upload() {
  local fname mid url; fname=$(basename "$1")
  local resp; resp=$(curl -fsS "${AUTH[@]}" -H "Content-Type: application/json" \
    -X POST "$API/social-sets/$SSID/media-uploads" \
    -d "$(jq -n --arg f "$fname" '{file_name:$f}')")
  mid=$(jq -r '.media_id' <<<"$resp"); url=$(jq -r '.upload_url' <<<"$resp")
  curl -fsS -o /dev/null -w '%{http_code}' -T "$1" "$url" | grep -qE '^20(0|4)$' \
    || { echo "S3 upload failed for $fname" >&2; return 1; }
  printf '%s' "$mid"
}
MID1=$(upload "$PNG1"); MID2=$(upload "$PNG2")
echo "media: msg1=$MID1 msg2=$MID2" >&2

# 2) Create the scheduled draft: X + Threads, 2-post thread, images inlined.
body=$(jq -n --arg m1 "$MSG1" --arg m2 "$MSG2" --arg a "$MID1" --arg b "$MID2" \
            --arg slot "$SLOT" --arg ver "$VER" '
{
  draft_title: ("Release " + $ver + " [auto]"),
  publish_at: $slot,
  platforms: {
    x:       {enabled:true, posts:[{text:$m1, media_ids:[$a]}, {text:$m2, media_ids:[$b]}]},
    threads: {enabled:true, posts:[{text:$m1, media_ids:[$a]}, {text:$m2, media_ids:[$b]}]}
  }
}')
resp=$(curl -fsS "${AUTH[@]}" -H "Content-Type: application/json" \
  -X POST "$API/social-sets/$SSID/drafts" -d "$body")
DRAFT_ID=$(jq -r '.id // .draft_id' <<<"$resp")
echo "OK: draft $DRAFT_ID scheduled $SLOT with 2 images" >&2
echo "$DRAFT_ID"
