---
metadata:
  harness: [claude]
  machines: both
  scope:
    projects: [google-play-cli]
name: gplay-social-cards
description: Generate gplay social media images (release announcements, terminal demos, info cards, hero) from the project's locked visual design system. Use when shipping a release, doing a feature spotlight, preparing a "did you know" post, or producing any social asset that needs gplay's brand visual. Output is always 1600×900 PNG.
---

# gplay-social-cards

Project-local skill. All outputs are 1600×900 PNG rendered via Chrome headless from HTML templates in `resources/`. The design system is locked — templates encode it; do not override.

## Workflow

1. Ask user: which type? (`release` | `terminal` | `info` | `hero`)
2. Ask user for the content (see slots per type below).
3. Copy `resources/template-<type>.html` → `~/Documents/gplay-marketing/visuals/<type>-<topic>.html`.
4. Edit ONLY the lines marked `<!-- EDIT -->` in the template. Never touch CSS / layout / palette.
5. Run `bash resources/render.sh <html-path> <png-path>`.
6. Return the absolute PNG path.

## Types and content slots

| Type | Required inputs |
|---|---|
| `release` | version string (e.g. `v0.1.0-alpha.3`) + 2-4 bullets, each `command` + `description` |
| `terminal` | command line + 1-8 output lines (mark success with `<span class="ok">`) + optional title bar version |
| `info` | title (1-3 words) + 5-9 rows (`code` + `arrow` + `desc` + optional inline `retry`/`norry` marker) + footer line |
| `hero` | rarely regenerated — only to update the public `docs/marketing/header.png`. Use a PR, not auto-commit. |

## Design system — non-negotiable

**Allow**
- Background `#050507` only
- Font `Google Sans Code` only — weight 400 / 500 / 700 for hierarchy
- Accent `#3DDC84` (Android green)
- Iridescence: top-right wash only. Palette `rgba(255,110,195,α)` magenta + `rgba(130,175,255,α)` blue. Hero α ≥ 0.55, content cards α ≤ 0.32.
- Border-radius 20px (terminals, cards)
- Text scale: 32 / 24 / 20 / 14 px (huge / body / label / chrome)

**Ban**
- DM Sans · Outfit · Plus Jakarta Sans · Space Grotesk · Inter · Manrope · Space Mono — all reflex-reject
- Gradient text (`background-clip: text` + gradient background) — absolute ban
- Tracked uppercase kickers ("JUST SHIPPED", "WHAT'S NEW", "DESIGN DECISION", "GPLAY · …")
- Pill-box badges — use plain colored text (`<span class="retry">` green, `<span class="norry">` red)
- Pure `#000` or `#ffffff`
- Iridescence anywhere other than top-right

## Output paths

- Default: `~/Documents/gplay-marketing/visuals/<type>/<type>-<topic>.png` (out of
  repo by convention; one subdir per type — `release/`, `terminal/`, `hero/`,
  `info/`). `mkdir -p` the subdir. Keep the HTML source beside its PNG.
- Exception: hero PNG updating the public README → `docs/marketing/header.png` inside the repo; ship via PR
- Naming: `<type>-<topic>.png` — examples: `release/release-alpha3.png`, `terminal/terminal-json.png`, `info/exit-codes-card.png`

## Voice

See [POSITIONING.md](../../../docs/marketing/POSITIONING.md). Builder-honest, technically precise, no marketing prose inside the cards. Every word earns its place.
