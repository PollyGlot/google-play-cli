// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import tailwindcss from '@tailwindcss/vite';
import starlightLlmsTxt from 'starlight-llms-txt';
import rehypeBaseLinks from './scripts/rehype-base-links.mjs';

// The site is served from gplay.sh (a Cloudflare Worker with static assets,
// see deploy/gplay.sh/ and ADR-0025). SITE_URL/SITE_BASE stay overridable so a
// preview build can target a different origin/base; everything (canonical URLs,
// sitemap, llms.txt, internal links) follows from the pair.
const SITE = process.env.SITE_URL ?? 'https://gplay.sh';
const BASE = (process.env.SITE_BASE ?? '/').replace(/\/$/, '');

export default defineConfig({
  site: SITE,
  base: BASE === '' ? '/' : BASE,
  trailingSlash: 'ignore',
  markdown: {
    // Keep CLI flags verbatim in prose: smartypants would turn `--track`
    // into "–track" (en dash), silently corrupting copy-pasteable text.
    smartypants: false,
    rehypePlugins: [[rehypeBaseLinks, { base: BASE }]],
  },
  integrations: [
    starlight({
      title: 'gplay',
      description:
        'gplay is a fast, dependency-free CLI for the Google Play Developer API: releases, tracks, reviews, metadata, compliance and team management from your terminal, CI pipeline, or AI agent.',
      logo: {
        // Per-theme mark: the glowing iridescent mark on dark, a deepened,
        // glow-free variant on light so it reads on white (ADR-0035).
        dark: './src/assets/logo-mark.svg',
        light: './src/assets/logo-mark-light.svg',
        alt: 'gplay: three forward chevrons',
      },
      favicon: '/favicon.svg',
      social: [
        {
          icon: 'github',
          label: 'GitHub',
          href: 'https://github.com/PollyGlot/google-play-cli',
        },
        {
          icon: 'seti:powershell',
          label: 'Agent skills',
          href: 'https://github.com/PollyGlot/google-play-cli-skills',
        },
      ],
      editLink: {
        baseUrl: 'https://github.com/PollyGlot/google-play-cli/edit/main/website/',
      },
      customCss: ['./src/styles/global.css', './src/styles/theme.css'],
      head: [
        {
          tag: 'meta',
          attrs: { property: 'og:image', content: `${SITE}${BASE}/og-default.png` },
        },
        {
          tag: 'meta',
          attrs: { name: 'twitter:card', content: 'summary_large_image' },
        },
        // Icon fallbacks the SVG favicon can't cover: iOS Safari ignores SVG
        // icons (an "Add to Home Screen" would screenshot the page instead),
        // and clients that request /favicon.ico blind (feed readers, crawlers,
        // older browsers) never read this head at all.
        {
          tag: 'link',
          attrs: { rel: 'icon', href: `${BASE}/favicon.ico`, sizes: '32x32' },
        },
        {
          tag: 'link',
          attrs: { rel: 'apple-touch-icon', href: `${BASE}/apple-touch-icon.png`, sizes: '180x180' },
        },
        {
          tag: 'link',
          attrs: { rel: 'manifest', href: `${BASE}/site.webmanifest` },
        },
        // Responsive browser chrome: match the active theme on mobile.
        {
          tag: 'meta',
          attrs: { name: 'theme-color', media: '(prefers-color-scheme: dark)', content: '#050507' },
        },
        {
          tag: 'meta',
          attrs: { name: 'theme-color', media: '(prefers-color-scheme: light)', content: '#ffffff' },
        },
      ],
      sidebar: [
        {
          label: 'Getting started',
          items: [
            { label: 'What is gplay?', slug: 'docs' },
            { label: 'Installation', slug: 'docs/getting-started/installation' },
            { label: 'Quickstart', slug: 'docs/getting-started/quickstart' },
            {
              label: 'Service account setup',
              slug: 'docs/getting-started/service-account',
            },
          ],
        },
        {
          label: 'Concepts',
          items: [{ autogenerate: { directory: 'docs/concepts' } }],
        },
        {
          label: 'Guides',
          items: [{ autogenerate: { directory: 'docs/guides' } }],
        },
        {
          label: 'AI agents',
          items: [{ autogenerate: { directory: 'docs/agents' } }],
        },
        {
          label: 'CLI reference',
          collapsed: true,
          items: [{ autogenerate: { directory: 'docs/reference', collapsed: true } }],
        },
      ],
      plugins: [
        starlightLlmsTxt({
          projectName: 'gplay',
          description:
            'gplay: a single-binary Go CLI for the Google Play Developer API. Upload and stage releases, manage tracks and testers, sync store listings, reply to reviews, push Data Safety declarations, and administer team permissions from CI or AI agents.',
          details:
            'gplay is open source (MIT), distributed as one static binary, and designed agent-first: JSON output mirrors the Google Play Developer API responses, exit codes are semantic (retry-safe vs terminal), and every command is non-interactive.',
          customSets: [
            {
              label: 'CLI command reference',
              description: 'Generated reference for every gplay command, flag, and exit code',
              paths: ['docs/reference/**'],
            },
          ],
        }),
      ],
    }),
  ],
  vite: {
    plugins: [tailwindcss()],
  },
});
