# gplay — brand assets

Visual identity for `gplay`. Two marks, used **separately** — never combined
in a single lockup.

- **Wordmark** `gplay_` — for headers, banners, social cards (wide, text-led).
- **Symbol** (three forward chevrons `>>>`) — for icons: avatars, favicon, the
  bot account (square, cropped to a circle by most platforms).

Shared signature: the **green cursor** (`_` in the wordmark, the lit green
chevron in the symbol). Locked design system: background `#050507`, font
*Google Sans Code*, accent `#3DDC84`, top-right magenta→blue→cyan iridescence.
See [../marketing/POSITIONING.md](../marketing/POSITIONING.md) for voice.

## Rule

> **Chevrons OR wordmark — never side-by-side in one image.** The symbol is the
> icon; the wordmark is the name. Don't lock them together.

## Files

| File | Use |
|---|---|
| `wordmark-dark.png` | README / banner header (dark) |
| `wordmark-transparent.png` | Wordmark over any surface |
| `social-preview.png` (1280×640) | GitHub repo social preview · X/Threads link card |
| `logo-symbol.svg` / `.png` · `avatar-460.png` | Owner/org avatar, X, Threads (square → circle) |
| `symbol-bot.svg` / `.png` | `gplay-bot` account avatar (green badge = automated) |
| `logo-favicon.svg` · `favicon-32.png` · `favicon-16.png` | Favicon (single chevron, crisp small) |
| `logo-mark-transparent.svg` | Symbol only, transparent |
| `logo-symbol-mono-{white,green,ink}.svg` | Single-color symbol (dark / brand stamp / light & print) |

SVGs are the masters — re-export any size from them.

## Manual steps (GitHub web UI — cannot be set via git)

- [ ] **Owner avatar** (PollyGlot): Settings → Profile → upload `avatar-460.png`
- [ ] **Repo social preview**: repo Settings → Social preview → upload `social-preview.png`
- [ ] **Bot avatar** (`gplay-bot` GitHub App): App settings → Display information → upload `symbol-bot.png`
