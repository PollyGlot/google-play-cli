// Rehype plugin: prefix the Astro `base` path onto root-absolute hrefs in
// Markdown content (`/docs/...` → `/google-play-cli/docs/...`).
//
// Starlight resolves its own navigation against `base`, but raw Markdown
// links are emitted verbatim — without this, every internal link breaks on
// GitHub Pages project hosting. When the site moves to gplay.sh (base "/"),
// the plugin becomes a no-op.

export default function rehypeBaseLinks({ base }) {
  const prefix = base.endsWith('/') ? base.slice(0, -1) : base;
  const apply = (node) => {
    if (
      node.type === 'element' &&
      node.tagName === 'a' &&
      typeof node.properties?.href === 'string'
    ) {
      const href = node.properties.href;
      if (href.startsWith('/') && !href.startsWith('//') && !href.startsWith(`${prefix}/`)) {
        node.properties.href = prefix + href;
      }
    }
    for (const child of node.children ?? []) apply(child);
  };
  return prefix === '' ? () => {} : (tree) => apply(tree);
}
