# Website light/dark/system theming with dark code islands

## Status

accepted

## Context

The gplay website ([`website/`](../../website/), Astro + Starlight, served from
the install Worker per [ADR-0025](0025-website-served-from-install-worker.md))
was built dark-only, but the two halves of the site disagree on what that means:

- **Docs (Starlight).** The default `<ThemeSelect>` is wired and functional —
  Auto / Light / Dark, default **Auto** (follows the OS). A light accent palette
  already exists in [`theme.css`](../../website/src/styles/theme.css)
  (`:root[data-theme='light'] { --sl-color-accent: #1d9457; … }`), and code
  blocks use Starlight's default dual (light+dark) Expressive Code theme — no
  custom single-theme override pins them dark. So the docs already render a
  coherent light mode; it had simply never been the headline.
- **Landing (`src/pages/index.astro`).** A single hand-written page that
  **hardcodes dark**: `<html class="scheme-dark">`, a hardcoded
  `theme-color: #050507`, and ~50 literal Tailwind dark utilities (`bg-ink`,
  `text-zinc-50/200/400/500`, `border-zinc-800/900`, `bg-zinc-950/60`, inline
  radial-glow gradients). It has its own custom header (not the Starlight nav),
  so it shows no theme selector and ignores the user's choice entirely.

The brand mark compounds it: `logo-mark.svg` (three chevrons, pale blue
`#82afff` @ 0.45 → cyan `#96e1f0` @ 0.78 → glowing green `#3ddc84`, no
background) is the header logo for **both** the landing and the Starlight docs.
On a light background the pale-blue chevron all but disappears and the glow — a
radial halo designed for near-black — falls flat.

The ask: make the whole site support a light theme. Concretely, offer the same
three modes everywhere with the OS-driven default, and unlock the landing so it
honors that choice.

## Decision

**Light / Dark / System everywhere, System as the default — the landing joins
the theme system the docs already have, and the terminal stays dark in both.**

1. **Three modes, System default.** This is Starlight's native `<ThemeSelect>`
   behavior (Auto/Light/Dark, default Auto). The docs already do this; **no docs
   theme change is required**. The work is to make the landing stop forcing dark
   and follow the same signal.

2. **The landing honors the shared theme signal.** Drop `class="scheme-dark"`.
   Add the same first-paint inline script Starlight uses — read
   `localStorage['starlight-theme']`, else `prefers-color-scheme` — and set
   `data-theme` on `<html>` **before paint** to avoid a flash. The landing's
   custom header gains a small Light/Dark/System control that writes the **same
   `localStorage['starlight-theme']` key**, so a choice made on the home page
   carries into the docs and vice-versa. One preference, one storage key, no
   second toggle behavior to keep in sync.

3. **Terminal and code blocks are *dark islands*.** *Dark island* (new term):
   a region that stays dark in **every** theme because its content is honestly
   dark — a terminal session, a shell snippet, a code block. The hero terminal
   mockup and the inline `$ npx …`/`$ brew …` snippets keep their black surface
   and their neon-green `$` prompt under light mode, the way Tailwind, Vercel,
   and Stripe keep code dark on light pages. This preserves the terminal ADN and
   keeps contrast high, and it is simply truthful: a real terminal is dark.

4. **Brand green splits by surface, for contrast.** Neon `#3ddc84` fails WCAG AA
   as text on white. So:
   - **Neon `#3ddc84`** stays for **solid fills** (the CTA button, where text
     sits *on* the green) and **inside dark islands** (the `$` prompt, terminal
     accents).
   - **Accessible `#1d9457`** — the green light mode already uses for the docs
     accent — becomes the **text/link/small-accent** green on light surfaces
     (links, stat numbers, the highlighted word in the H1, hovers). Reusing the
     existing `--sl-color-accent` light value keeps docs and landing on one
     green, no third shade to maintain.

5. **A dedicated light logo variant.** Add `logo-mark-light.svg`: the same three
   chevrons with **deepened iridescence** (a denser blue, a teal, a darker
   green) and the **glow filter removed**, so the mark reads as itself on white
   instead of washing out. Wire it per theme:
   - **Docs** — via Starlight's native `logo: { light, dark }` config
     (`dark: logo-mark.svg`, `light: logo-mark-light.svg`). Free, no override.
   - **Landing** — a theme-aware swap (`<picture>`/CSS) in the custom header.
   The favicon (a self-contained dark tile) and the OG/social PNG (a fixed dark
   image) are unaffected.

6. **Hero radial glows are dark-only.** The two subtle blue/green radial
   gradients depend on a near-black backdrop; their low alphas read as muddy
   smudges on white. They are **removed in light** (clean white hero) and kept in
   dark.

7. **Recolor the landing with semantic CSS tokens, not Tailwind `dark:`.**
   Define a small token set in `theme.css` with light/dark values
   (`--surface`, `--surface-panel`, `--text`, `--text-muted`, `--border`, and an
   accent token), expose them to Tailwind via `@theme`, and replace the literal
   dark utilities (`bg-ink` → `bg-surface`, `text-zinc-400` → `text-muted`, …).
   One source of truth, aligned with Starlight's own `--sl-color-*` variables and
   the existing brand `@theme` palette. Dark-island elements keep **literal** dark
   classes so they never flip.

8. **`theme-color` meta becomes responsive.** Replace the single hardcoded
   `#050507` (present in both `index.astro` and the Starlight `head` config) with
   light/dark `<meta name="theme-color" media="…">` pair so the mobile browser
   chrome matches the active theme.

## Why this shape

1. **System default respects the visitor, not the brand's mood.** Defaulting to
   the OS preference (rather than "light by default" or staying "dark by
   default") means nobody is yanked out of their chosen environment on first
   paint. The brand's dark identity is still the experience for the majority who
   browse dark — it is no longer *imposed* on those who don't.

2. **Dark islands keep the identity while going light.** The terminal is the
   product's whole pitch; inverting it to a light code theme would both look
   wrong and cost a second syntax palette to tune. Keeping code dark on a light
   page is a well-trodden, legible pattern and the cheapest faithful answer.

3. **One green, two roles — no contrast debt, no palette sprawl.** Splitting by
   surface (fills/islands vs light text) rather than inventing a landing-specific
   green keeps WCAG AA on light without adding a color to the system; it reuses
   the shade the docs already ship.

4. **Tokens over `dark:` variants for a brand surface.** Both touch most lines.
   Tokens put the two palettes in one file next to the existing `--sl-color-*`
   and brand `@theme` vars — the project already thinks in design tokens — and
   keep the markup readable instead of doubling every class. The indirection
   (values live in `theme.css`) is the accepted cost.

## What we accept

- **A second brand asset to maintain.** `logo-mark-light.svg` must stay faithful
  to the locked mark; a future mark change touches two files. Accepted for a
  header logo that otherwise looks broken on white.
- **Token indirection.** Landing palette values move out of the markup into
  `theme.css`. Accepted for the single-source-of-truth and docs/landing
  coherence.
- **A toggle behavior owned outside Starlight.** The landing's selector
  replicates a few lines of theme-write logic. It is bound to Starlight's exact
  storage key, so the risk is a small, well-contained one.

## Considered and rejected

- **Light-only (drop dark).** Discards the terminal-dark identity wholesale.
  Rejected — the dark aesthetic is core positioning, not a default to delete.
- **Light by default (light first paint, toggle to dark).** Changes the brand's
  first impression for everyone and ignores the OS signal. Rejected in favor of
  **System** default.
- **Light terminal / light code blocks.** Inverts the dark islands; loses the
  terminal ADN and needs a second, separately-tuned syntax palette. Rejected for
  the dark-island rule.
- **Keep neon `#3ddc84` as text on light.** Fails WCAG AA; links and stats become
  hard to read on white. Rejected for the accessible `#1d9457` split.
- **Tailwind `dark:` variants instead of tokens.** Idiomatic and zero-indirection
  but doubles the class list on ~every element and scatters the two palettes
  across the markup. Rejected for semantic tokens.
- **Keep the dark-only logo on light.** Washes out the pale-blue chevron and
  flattens the glow. Rejected for the dedicated light variant.

## Relation to prior decisions

Extends [ADR-0025](0025-website-served-from-install-worker.md) (the site's
hosting/shape) on the presentation layer; no change to hosting, the install
Worker, the generated CLI reference, or the `llms.txt` surface. Per the project's
release-type discipline, this ships as a `docs(site)`/`chore(site)` change — a
presentation change that does not bump the CLI binary version.
