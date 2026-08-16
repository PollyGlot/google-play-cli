#!/usr/bin/env bash
# render-loop.sh — rend une scène de terminal-loop.html en MP4 prêt à publier
# (X/Threads), sans capture écran : frames PNG déterministes via Chrome
# headless (?t=<ms>), assemblées par ffmpeg.
#
# Usage: ./render-loop.sh [scene] [out.mp4] [fps]
#   scene : une clé de SCENES dans terminal-loop.html (défaut : upload)
#   out   : défaut ~/Documents/gplay-marketing/visuals/terminal/terminal-<scene>-1080x1350.mp4
#   fps   : défaut 30
set -euo pipefail

SCENE="${1:-upload}"
HERE="$(cd "$(dirname "$0")" && pwd)"
HTML="$HERE/terminal-loop.html"
OUTDIR="$HOME/Documents/gplay-marketing/visuals/terminal"
OUT="${2:-$OUTDIR/terminal-$SCENE-1080x1350.mp4}"
FPS="${3:-30}"
W=1080; H=1350
CHROME="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"

[ -x "$CHROME" ] || { echo "Chrome introuvable : $CHROME" >&2; exit 1; }
[ -r "$HTML" ]   || { echo "Player introuvable : $HTML" >&2; exit 1; }
command -v ffmpeg >/dev/null || { echo "ffmpeg requis (brew install ffmpeg)" >&2; exit 1; }
mkdir -p "$(dirname "$OUT")"

# Durée exacte de la boucle, publiée par le player dans data-loop-ms.
LOOP_MS="$("$CHROME" --headless --disable-gpu --dump-dom \
  "file://$HTML?scene=$SCENE&t=0" 2>/dev/null \
  | grep -o 'data-loop-ms="[0-9]*"' | grep -o '[0-9]*' | head -1)"
[ -n "$LOOP_MS" ] || { echo "data-loop-ms introuvable (scène '$SCENE' ?)" >&2; exit 1; }

N=$(( (LOOP_MS * FPS + 999) / 1000 ))
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
echo "scène=$SCENE  boucle=${LOOP_MS}ms  ${N} frames @ ${FPS}fps  → $OUT"

# Frames en 2× (2160×2700) pour un anticrénelage propre après downscale.
# NB : surtout pas de --user-data-dir ici — un profil neuf fait pendre le
# Chrome headless de macOS indéfiniment ; le headless moderne crée de toute
# façon un profil éphémère par instance, donc le parallélisme est sûr.
# Watchdog : Chrome headless pend sporadiquement (~1 frame sur 100) → chaque
# frame a 30 s max par tentative, 3 tentatives, puis passe de rattrapage série.
export CHROME HTML SCENE TMP FPS W H
FRAME_SCRIPT='
  i=$1
  T=$(( i * 1000 / FPS ))
  out="$TMP/f_$(printf %05d "$i").png"
  for attempt in 1 2 3; do
    "$CHROME" --headless --disable-gpu --hide-scrollbars \
      --force-device-scale-factor=2 --window-size=${W},${H} \
      --virtual-time-budget=1200 \
      --screenshot="$out" \
      "file://$HTML?scene=$SCENE&t=$T" >/dev/null 2>&1 &
    cpid=$!
    n=0
    while kill -0 "$cpid" 2>/dev/null && [ "$n" -lt 30 ]; do sleep 1; n=$((n+1)); done
    if kill -0 "$cpid" 2>/dev/null; then
      kill -9 "$cpid" 2>/dev/null
      wait "$cpid" 2>/dev/null
    fi
    [ -s "$out" ] && exit 0
  done
  echo "⚠ frame $i en échec après 3 tentatives" >&2
  exit 1
'
seq 0 $((N - 1)) | xargs -P 8 -n 1 bash -c "$FRAME_SCRIPT" _ || true

# Rattrapage série des frames manquantes (hangs tués par le watchdog).
for i in $(seq 0 $((N - 1))); do
  [ -s "$TMP/f_$(printf %05d "$i").png" ] || bash -c "$FRAME_SCRIPT" _ "$i" || true
done

MISSING=$(( N - $(ls "$TMP"/f_*.png 2>/dev/null | wc -l | tr -d " ") ))
[ "$MISSING" -eq 0 ] || { echo "⚠ $MISSING frame(s) manquante(s)" >&2; exit 1; }

ffmpeg -y -loglevel error -framerate "$FPS" -i "$TMP/f_%05d.png" \
  -vf "scale=$W:$H:flags=lanczos" \
  -c:v libx264 -profile:v high -crf 17 -pix_fmt yuv420p -movflags +faststart \
  -an "$OUT"

echo "→ $OUT ($(du -h "$OUT" | cut -f1 | tr -d ' '), ${LOOP_MS}ms, ${W}×${H})"
