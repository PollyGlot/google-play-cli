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

The site is served from **gplay.sh** by a single Cloudflare Worker that carries
the built site as static assets and also serves the dynamic `/install`
endpoint. `dist/` is uploaded by `.github/workflows/deploy-site.yml` on push to
`main` touching `website/` or `deploy/gplay.sh/`, on every published release (to
refresh the generated reference), and via *workflow dispatch*. The Worker, its
config, and the docs/www redirects live in [`deploy/gplay.sh/`](../deploy/gplay.sh/).
Rationale: [ADR-0025](../docs/adr/0025-website-served-from-install-worker.md).

The site/base pair defaults to `https://gplay.sh` + `/`. Override it for a
preview build on a different origin:

```sh
SITE_URL=https://preview.example.com SITE_BASE=/ npm run build
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
