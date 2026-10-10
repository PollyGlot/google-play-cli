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

body=$(cat)
# The key is read from the environment, never passed as an argument, so it does
# not show in the runner's process list.
sig=$(BODY="$body" python3 -I -c '
import hashlib, hmac, os
print(hmac.new(os.environ["SIGNING_SECRET"].encode(), os.environ["BODY"].encode(), hashlib.sha256).hexdigest())
')

curl -sS --max-time 30 -o "$out" -w '%{http_code}' \
  -X POST -H 'Content-Type: application/json' \
  -H "X-Hub-Signature-256: sha256=$sig" \
  --data-binary "$body" "$WEBHOOK_URL"
