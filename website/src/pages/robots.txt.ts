import type { APIRoute } from 'astro';

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
  const origin = site?.origin ?? 'https://gplay.sh';

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
