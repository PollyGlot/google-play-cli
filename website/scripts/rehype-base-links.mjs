// Rehype plugin: prefix the Astro `base` path onto root-absolute hrefs in
// Markdown content (e.g. with base "/preview": `/docs/...` → `/preview/docs/...`).
//
// Starlight resolves its own navigation against `base`, but raw Markdown
// links are emitted verbatim — without this, every internal link would break
// under a non-root base. At the gplay.sh apex (base "/") the plugin is a no-op;
// it only does work for a preview build served under a sub-path.

export default function rehypeBaseLinks({ base }) {
  const prefix = base.endsWith('/') ? base.slice(0, -1) : base;
  const apply = (node) => {
    if (
      node.type === 'element' &&
      node.tagName === 'a' &&
      typeof node.properties?.href === 'string'
    ) {
      const href = node.properties.href;
      if (
        href.startsWith('/') &&
        !href.startsWith('//') &&
        href !== prefix &&
        !href.startsWith(`${prefix}/`)
      ) {
        node.properties.href = prefix + href;
      }
    }
    for (const child of node.children ?? []) apply(child);
  };
  return prefix === '' ? () => {} : (tree) => apply(tree);
}
