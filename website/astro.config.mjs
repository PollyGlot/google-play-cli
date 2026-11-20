// @ts-check
import { defineConfig } from 'astro/config';
import { unified } from '@astrojs/markdown-remark';
import starlight from '@astrojs/starlight';
import tailwindcss from '@tailwindcss/vite';
import starlightLlmsTxt from 'starlight-llms-txt';
import rehypeBaseLinks from './scripts/rehype-base-links.mjs';
import cspMeta from './scripts/csp.mjs';
import { THEME_COLOR } from './src/theme-color.mjs';

const SITE = process.env.SITE_URL ?? 'https://gplay.sh';
const BASE = (process.env.SITE_BASE ?? '/').replace(/\/$/, '');

export default defineConfig({
  site: SITE,
  base: BASE === '' ? '/' : BASE,
  trailingSlash: 'ignore',
  compressHTML: true,
  markdown: {
    processor: unified({
      smartypants: false,
      rehypePlugins: [[rehypeBaseLinks, { base: BASE }]],
    }),
  },
  integrations: [
    starlight({
      title: 'gplay',
      description:
        'gplay is a fast, dependency-free CLI for the Google Play Developer API: releases, tracks, reviews, metadata, compliance and team management from your terminal, CI pipeline, or AI agent.',
      logo: {
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
        {
          tag: 'meta',
          attrs: { name: 'theme-color', media: '(prefers-color-scheme: dark)', content: THEME_COLOR.dark },
        },
        {
          tag: 'meta',
          attrs: { name: 'theme-color', media: '(prefers-color-scheme: light)', content: THEME_COLOR.light },
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
          items: [
            { autogenerate: { directory: 'docs/guides' } },
            { label: 'Migrating to 2.0', slug: 'docs/migration/2.0' },
            { label: 'Migrating from fastlane supply', slug: 'docs/migration/fastlane-supply' },
          ],
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
    cspMeta(),
  ],
  vite: {
    plugins: [tailwindcss()],
  },
});
