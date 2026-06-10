// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import react from '@astrojs/react';
import tailwindcss from '@tailwindcss/vite';
import starlightLlmsTxt from 'starlight-llms-txt';
import rehypeBaseLinks from './scripts/rehype-base-links.mjs';

// Interim hosting is GitHub Pages project hosting (draft/beta — issue #86).
// When the gplay.sh domain is live, deploy with:
//   SITE_URL=https://gplay.sh SITE_BASE=/
// and everything (canonical URLs, sitemap, llms.txt, internal links) follows.
const SITE = process.env.SITE_URL ?? 'https://pollyglot.github.io';
const BASE = (process.env.SITE_BASE ?? '/google-play-cli').replace(/\/$/, '');

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
        'gplay is a fast, dependency-free CLI for the Google Play Developer API — releases, tracks, reviews, metadata, compliance and team management from your terminal, CI pipeline, or AI agent.',
      logo: {
        src: './src/assets/logo-mark.svg',
        alt: 'gplay — three forward chevrons',
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
        { tag: 'meta', attrs: { name: 'theme-color', content: '#050507' } },
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
            'gplay — a single-binary Go CLI for the Google Play Developer API. Upload and stage releases, manage tracks and testers, sync store listings, reply to reviews, push Data Safety declarations, and administer team permissions from CI or AI agents.',
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
    react(),
  ],
  vite: {
    plugins: [tailwindcss()],
  },
});
