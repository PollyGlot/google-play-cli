#!/usr/bin/env bash
# Render a gplay social-card HTML to a 1600×900 PNG via Chrome headless.
# Usage: render.sh <input.html> <output.png>
set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo "Usage: $0 <input.html> <output.png>" >&2
  exit 2
fi

INPUT="$1"
OUTPUT="$2"
CHROME="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"

[ -x "$CHROME" ] || { echo "Chrome not found at: $CHROME" >&2; exit 1; }
[ -r "$INPUT"  ] || { echo "Input not readable: $INPUT" >&2; exit 1; }

# Absolute paths (required for file:// URL and reliable -screenshot writes)
INPUT_ABS="$(cd "$(dirname "$INPUT")" && pwd)/$(basename "$INPUT")"
OUTDIR="$(dirname "$OUTPUT")"
mkdir -p "$OUTDIR"
OUTPUT_ABS="$(cd "$OUTDIR" && pwd)/$(basename "$OUTPUT")"

"$CHROME" \
  --headless --disable-gpu --hide-scrollbars \
  --window-size=1600,900 \
  --virtual-time-budget=10000 \
  --screenshot="$OUTPUT_ABS" \
  "file://$INPUT_ABS" 2>&1 | grep -E "bytes|error" || true

[ -f "$OUTPUT_ABS" ] || { echo "Render failed: $OUTPUT_ABS not produced" >&2; exit 1; }
echo "→ $OUTPUT_ABS"
