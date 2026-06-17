import type { APIRoute, GetStaticPaths } from 'astro';
import { getCollection } from 'astro:content';

// Emits a plain-Markdown copy of every docs page at `<page-path>.md`, e.g.
// /docs/reference/apps/ → /docs/reference/apps.md and /docs/ → /docs/index.md.
// These are the artifacts the gplay.sh Worker serves under content negotiation
// when an agent sends `Accept: text/markdown` (see deploy/gplay.sh/worker.js),
// and they are directly fetchable too. The landing page (/) is not a docs
// entry; its Markdown view is the generated llms.txt, mapped in the Worker.
//
// `slug` is the collection entry id, which already carries the `docs/` prefix
// (content lives under src/content/docs/docs/**), so it mirrors the public URL.

export const getStaticPaths: GetStaticPaths = async () => {
  const docs = await getCollection('docs');
  return docs.map((entry) => ({
    params: { slug: entry.id },
    props: { title: entry.data.title, body: entry.body ?? '' },
  }));
};

export const GET: APIRoute = ({ props }) => {
  const { title, body } = props as { title: string; body: string };
  const markdown = `# ${title}\n\n${body}`.replace(/\s+$/, '') + '\n';
  return new Response(markdown, {
    headers: { 'Content-Type': 'text/markdown; charset=utf-8' },
  });
};
