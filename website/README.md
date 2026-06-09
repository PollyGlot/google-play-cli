# gplay website

The gplay landing page + documentation site ([issue #86](https://github.com/PollyGlot/google-play-cli/issues/86)),
built with [Astro](https://astro.build) + [Starlight](https://starlight.astro.build),
React islands, and Tailwind 4.

## Develop

```sh
npm install
npm run dev        # generates the CLI reference, then serves on :4321
npm run build      # same generation, then a production build into dist/
```

Both `dev` and `build` first run `scripts/gen-reference.mjs`, which executes
`bin/gplay … --help` recursively and writes one reference page per command
into `src/content/docs/docs/reference/` (gitignored — never edit those by
hand). If `bin/gplay` is missing, the script builds it with `go build`.

## Hosting

Interim hosting is **GitHub Pages** (`.github/workflows/website.yml`):
deploys on push to `main` touching `website/`, on every published release
(to refresh the generated reference), and manually via *workflow dispatch*.

The site/base pair defaults to `https://pollyglot.github.io` +
`/google-play-cli`. When the `gplay.sh` domain is live, deploy with:

```sh
SITE_URL=https://gplay.sh SITE_BASE=/ npm run build
```

Canonical URLs, sitemap, robots.txt, llms.txt, and every internal link
follow automatically (`scripts/rehype-base-links.mjs` prefixes the base
onto root-absolute Markdown links).

## GEO / AI-agent surface

- `/llms.txt`, `/llms-small.txt`, `/llms-full.txt` — via `starlight-llms-txt`,
  including the full generated command reference as a custom set.
- `/robots.txt` — generated endpoint, explicitly allowing AI crawlers.
- JSON-LD (`SoftwareApplication`, `FAQPage`, `WebSite`) on the landing page.
- Sitemap + per-page meta descriptions via Starlight.

## Content rules

- Site content is **English** (matches the repo's public artifacts).
- Demo output in marketing surfaces must be format-faithful to the real CLI
  (e.g. the landing terminal mirrors `releases upload`'s `renderTable`).
- Command behavior claims must be verifiable in `--help` or the source —
  the generated reference exists so prose never has to restate flags.
