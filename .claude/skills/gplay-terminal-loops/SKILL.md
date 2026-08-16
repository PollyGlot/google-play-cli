---
metadata:
  harness: [claude]
  machines: both
  scope:
    projects: [google-play-cli]
name: gplay-terminal-loops
description: Generate gplay animated terminal demo loops (typing + real CLI output) and render them to publication-ready MP4s for X/Threads, without screen recording. Use when preparing an animated demo clip for a gplay command, a feature spotlight video, or any social asset that needs gplay's terminal-in-motion visual. Output is a seamless ~10-15 s loop, 1080×1350 H.264.
---

# gplay-terminal-loops

Project-local skill, sibling of `gplay-social-cards` (static PNGs). Same locked
design system, but animated: a deterministic multi-scene player
(`resources/terminal-loop.html`) plus a frame-by-frame renderer
(`resources/render-loop.sh`). One scene = one command demo = one MP4.

## Workflow — adding a scene for a command

1. **Verify the real behavior first** (non-negotiable, see Honesty below):
   - `go run ./cmd/gplay <ns> <cmd> --help` for the exact usage/flags.
   - Read the command's render code (`commands/<ns>/<cmd>/…renderTable`) or the
     stderr `Fprintf` lines, and copy the output **verbatim** — spacing,
     casing, quirks included.
2. **Add the scene** to the `SCENES` registry in `resources/terminal-loop.html`
   (schema below). Pick format-faithful demo data, zero real PII.
3. **Preview**: open the file with `?scene=<key>` (live loop) and
   `?scene=<key>&t=<ms>` (frozen frame). Click/keypress restarts the loop.
4. **Render**: `resources/render-loop.sh <key>` →
   `~/Documents/gplay-marketing/visuals/terminal/terminal-<key>-1080x1350.mp4`.
5. Eyeball the MP4 (first/mid/last frames) before publishing.

## Scene schema

```js
SCENES.mykey = {
  cmd: 'gplay tracks list',        // typed after the prompt — must be a real invocation
  execMs: 600,                      // silent run time (network call ≈ 800-1200, local ≈ 400-600)
  out: [                            // one entry per printed line, in order
    [['m','key:     '],['','value']],   // spans: [class, text]
    [['ok','✓ only if the CLI really prints it']],
  ],
  exitCheck: false,                 // true → adds the `echo $?` → 0 beat
};
```

Span classes: `''` foreground, `'m'` muted (table keys, secondary), `'ok'`
green (only for ✓ lines the CLI actually emits). Prompt is fixed:
`~/dev/comet $` (path muted, `$` green). Font auto-fits the longest line —
keep lines ≤ 80 chars or the type gets small on phones.

## Honesty rules (from the marketing-visual-honesty convention)

- Output lines are **verbatim** from the code or a real run. No invented
  flags, no invented lines, no prettified spacing.
- `CONFIG.successLine` stays **false**: the CLI does not print
  "✓ Uploaded …" on `releases upload` today. If that confirmation ships in
  gplay, flip it and add `okLine` per scene.
- Demo data is format-faithful (versionCode 142 / 1.4.2, service-account
  emails like `ci@demo-app.iam.gserviceaccount.com`), never real.

## Design system — locked, inherited from gplay-social-cards

Brand palette is the default and the only one for published assets:
page `#050507`, panel `#0c0c10`, accent/success `#3DDC84`, muted `#8a8a90`,
Google Sans Code (embedded as data-URI — offline-safe, frame-stable),
terminal frame with traffic lights + `gplay` title, iridescence top-right
only (α ≤ 0.32), radius 20px. `?palette=github` and `?frame=0` exist for
experiments, not for publishing.

## Player / renderer contract

- The loop is **deterministic** (seeded RNG): every iteration is
  pixel-identical, so `?t=<ms>` frozen frames assemble into the exact loop.
  Cursor blink and the end fade are computed analytically in freeze mode.
- The player publishes the loop duration in `<body data-loop-ms>`;
  render-loop.sh reads it, renders `loop_ms × fps / 1000` frames at 2×
  (2160×2700) and downsamples to 1080×1350 (4:5 — best X/Threads real
  estate; loops seamlessly: the content fades inside the persistent panel).
- Publication specs: H.264 high, `yuv420p`, CRF 17, 30 fps, faststart, no
  audio. GIF if ever needed:
  `ffmpeg -i in.mp4 -vf "fps=18,scale=720:-1:flags=lanczos,split[a][b];[a]palettegen[p];[b][p]paletteuse=dither=bayer:bayer_scale=4" out.gif`

## Scene backlog (verify each before adding)

Shipped: `upload` (releases upload → draft-by-default story, ADR-0002),
`login` (auth login → real green ✓). Candidates, one flagship per namespace:
`releases promote` (--confirm story) · `releases rollout` (staged fraction) ·
`tracks list` · `apps list` (pinned ✓ row) · `auth status` (keystore backend) ·
`reviews list` / `reviews reply` · `testers add` · `metadata pull` ·
`schema get --output json | jq` (agent/CI story, ADR-0003 pass-through).

## Website embedding (future)

The player is dependency-free vanilla JS — the natural path for gplay.sh is
an Astro island in `website/` reusing the same engine with `SCENES` exported
as JSON (no MP4s on the web: crisper, lighter, theme-consistent). Decision
belongs in an issue on the main repo; don't silently add it.
