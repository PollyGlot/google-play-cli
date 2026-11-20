# ADR-0069: How the website is built and served, and the platform behaviours it absorbs

## Status

accepted (2026-11-20)

## Context

The website ([`website/`](../../website/), Astro and Starlight) is built into
static assets and served by the `gplay.sh` Worker
([ADR-0025](./0025-website-served-from-install-worker.md)), with its theming
and Content-Security-Policy in
[ADR-0035](./0035-website-light-dark-system-theming.md). Around those choices,
the build scripts, the Astro config and the Worker each absorb a behaviour of
Astro, Starlight, a browser or Cloudflare that the code cannot show. These
decisions lived in code comments; the comment purge (the
`scripts/lint/comments.py` ratchet) gathers them here.

This ADR is a catalogue. A new entry goes under the layer it concerns.

## Decision

### Cloudflare Workers

- **A response from `env.ASSETS.fetch` or `Response.redirect` has immutable
  headers.** Setting a header means re-wrapping it in a fresh `Response`
  first.
- **When routing throws, the Worker degrades to plain asset serving**, rather
  than a Cloudflare 1101 error page for the whole site.
- **Astro's `/_astro/*` assets are served `immutable` for a year.** They are
  content-hashed, so a changed file gets a new URL. Without the override, the
  asset binding's default (`max-age=0, must-revalidate`) costs a revalidation
  round trip, and a Worker invocation, per asset per page view.
- **The asset binding uses `auto-trailing-slash` HTML handling**: it redirects
  between `/a/` and `/a` to whichever file exists, so `/a`, `/a/` and
  `/a.html` all resolve.

### Security headers

- **The Worker sets a fixed set of security headers on every response**
  (static assets, `/install`, redirects). The Content-Security-Policy is not
  one of them: only the build knows the hashes of the inline scripts, so each
  page carries its own policy as a `<meta>` (ADR-0035), and the Worker's
  `x-frame-options: DENY` covers the framing a meta policy cannot express.
- **`cross-origin-opener-policy: same-origin`** severs `window.opener` between
  `gplay.sh` and any cross-origin page it opens or is opened by. The site has
  no cross-origin popup to keep talking to, so it costs nothing.

### Astro and Starlight

- **`compressHTML` stays `true`.** Astro 7's default (`'jsx'`) drops the
  whitespace between inline elements and shifts words in the landing and docs
  prose; `true` keeps the Astro 6 output.
- **Smart punctuation is off, on the Markdown processor itself.** Smartypants
  turns `--track` into an en dash plus "track", corrupting every copy-pasted
  command. It is set on the processor rather than through the deprecated
  `markdown.smartypants`, because Starlight pushes its plugins onto that same
  processor and `@astrojs/mdx` reads the setting from it, so `.md` and `.mdx`
  both follow.
- **`@astrojs/markdown-remark` is a direct dependency kept within the peer
  range of `astro`, Starlight and `@astrojs/mdx`.** Since Astro 7 it is an
  optional peer of all three, and the site's copy is the one they share.
- **Root-absolute links in Markdown get the Astro `base` prefixed**
  (`website/scripts/rehype-base-links.mjs`). Starlight resolves its own
  navigation against `base` but emits Markdown links verbatim, so every
  internal link would break under a sub-path. At the `gplay.sh` apex (base
  `/`) the plugin does nothing; it serves preview builds under a sub-path.
- **Tailwind's preflight is not imported.** Starlight owns the base styles of
  the docs, and the landing styles itself with utilities.
- **The head carries a `favicon.ico` and an `apple-touch-icon` beside the SVG
  favicon.** iOS Safari ignores SVG icons (Add to Home Screen would
  screenshot the page instead), and clients that request `/favicon.ico` blind
  (feed readers, crawlers, older browsers) never read the head.
- **The landing's theme control marks its active option with a class set by
  script**, not `:has(input:checked)`: that selector does not reliably
  re-evaluate when the radio changes at the same moment the theme tokens flip.

### Generated CLI reference (`website/scripts/gen-reference.mjs`)

- **The default binary is always rebuilt.** Reusing whatever `bin/gplay` an
  earlier checkout left would publish a stale reference from `npm run dev`;
  `go build` is incremental, so an up-to-date binary costs a cache hit. A
  binary path given explicitly is used as is.
- **Without a Go toolchain, an existing binary is used with a warning** that
  it may not match the sources, rather than blocking the docs.
- **Reference pages have no "Edit page" link.** They are generated into a
  gitignored directory, so the link would 404 on GitHub; the text to fix is
  the command's `--help` in the Go sources.
- **`<` is escaped in prose, outside code spans**, so Markdown never swallows
  a placeholder such as `<aab>` as inline HTML.

### Checks on the built site (`website/scripts/check-dist.mjs`)

The build lets three defects through, and this offline check catches them:

- **Internal links.** Every root-absolute, relative or same-origin `href` and
  `src` must resolve to a built file under the Worker's trailing-slash rules,
  and a `#fragment` must name an id on the target page. Starlight's `#_top`
  skip link targets the document itself. External links are not fetched.
- **Repo links.** A link to a `main` path of the repository on GitHub
  (`edit`, `blob`, `tree`) must point at a tracked path; this is what catches
  an "Edit page" link on a generated page.
- **Flag dashes.** No page may show a flag whose `--` became an en or em dash,
  which proves smart punctuation stayed off. The pattern requires the dash at
  a word start, so a prose range written with an en dash never matches.

## Consequences

- Most entries are tied to a version of Astro, Starlight or the Workers
  runtime; an upgrade of one is the moment to re-read its section.
- The checks on the built site run in CI, so a regression of the smart
  punctuation or link entries fails the build check rather than reaching
  `gplay.sh`.
