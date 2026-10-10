#!/usr/bin/env bash
# Fires a t3code webhook task (PRD #501 agents, t3code runtime). The task is a
# scheduled task of type "webhook" on the maintainer's always-on machine; its
# public relay URL accepts a POST whose body the task prompt reads through
# {{body.<field>}} placeholders. The task checks an HMAC-SHA256 of the raw body
# in the GitHub webhook format (X-Hub-Signature-256: sha256=<hex>), so a leaked
# URL alone cannot start a run.
#
# Usage: fire-t3-task.sh <response-file> < payload.json
#
# WEBHOOK_URL and SIGNING_SECRET must be in the environment. Prints the HTTP
# status code on stdout and leaves the response body in <response-file>; the
# URL is a secret, so nothing else is echoed. One attempt, no retry: a second
# delivery means a second run.
set -euo pipefail

out=${1:?usage: fire-t3-task.sh <response-file> < payload.json}
: "${WEBHOOK_URL:?WEBHOOK_URL is not set}"
: "${SIGNING_SECRET:?SIGNING_SECRET is not set}"

body_file=$(mktemp)
trap 'rm -f "$body_file"' EXIT
cat > "$body_file"
# The key is read from the environment, never passed as an argument, so it does
# not show in the runner's process list.
sig=$(BODY_FILE="$body_file" python3 -I -c '
import hashlib, hmac, os
with open(os.environ["BODY_FILE"], "rb") as f:
    print(hmac.new(os.environ["SIGNING_SECRET"].encode(), f.read(), hashlib.sha256).hexdigest())
')

# The URL and the signature go through a curl config on stdin, not argv: with
# both, anyone reading the process list could replay a valid request.
curl -sS --max-time 30 -o "$out" -w '%{http_code}' \
  -X POST -H 'Content-Type: application/json' \
  --data-binary "@$body_file" -K - <<CONFIG
url = "$WEBHOOK_URL"
header = "X-Hub-Signature-256: sha256=$sig"
CONFIG
