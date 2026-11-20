import type { APIRoute, GetStaticPaths } from 'astro';
import { getCollection } from 'astro:content';

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
