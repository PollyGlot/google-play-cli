#!/usr/bin/env bash
set -euo pipefail

out=${1:?usage: fire-t3-task.sh <response-file> < payload.json}
: "${WEBHOOK_URL:?WEBHOOK_URL is not set}"
: "${SIGNING_SECRET:?SIGNING_SECRET is not set}"

body_file=$(mktemp)
trap 'rm -f "$body_file"' EXIT
cat > "$body_file"
sig=$(BODY_FILE="$body_file" python3 -I -c '
import hashlib, hmac, os
with open(os.environ["BODY_FILE"], "rb") as f:
    print(hmac.new(os.environ["SIGNING_SECRET"].encode(), f.read(), hashlib.sha256).hexdigest())
')

curl -sS --max-time 30 -o "$out" -w '%{http_code}' \
  -X POST -H 'Content-Type: application/json' \
  --data-binary "@$body_file" -K - <<CONFIG
url = "$WEBHOOK_URL"
header = "X-Hub-Signature-256: sha256=$sig"
CONFIG
