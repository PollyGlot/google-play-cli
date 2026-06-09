import type { APIRoute } from 'astro';

// Everything on this site is public documentation — crawling is welcome,
// explicitly including AI/answer-engine crawlers (GEO). The AI bots are
// named individually so the intent survives any future tightening of the
// catch-all rule.
//
// Note: on GitHub Pages project hosting this file is served under the
// project base path, where most crawlers won't look for it. It becomes the
// real /robots.txt once the site moves to the gplay.sh apex.

const AI_CRAWLERS = [
  'GPTBot',
  'OAI-SearchBot',
  'ChatGPT-User',
  'ClaudeBot',
  'Claude-User',
  'Claude-SearchBot',
  'anthropic-ai',
  'PerplexityBot',
  'Perplexity-User',
  'Google-Extended',
  'Applebot-Extended',
  'Bytespider',
  'CCBot',
  'cohere-ai',
  'meta-externalagent',
  'MistralAI-User',
];

export const GET: APIRoute = ({ site }) => {
  const base = import.meta.env.BASE_URL.replace(/\/$/, '');
  const origin = site?.origin ?? 'https://pollyglot.github.io';

  const lines = [
    ...AI_CRAWLERS.flatMap((bot) => [`User-agent: ${bot}`, 'Allow: /', '']),
    'User-agent: *',
    'Allow: /',
    '',
    `Sitemap: ${origin}${base}/sitemap-index.xml`,
    '',
    `# LLM-friendly docs: ${origin}${base}/llms.txt`,
  ];

  return new Response(lines.join('\n') + '\n', {
    headers: { 'Content-Type': 'text/plain; charset=utf-8' },
  });
};
