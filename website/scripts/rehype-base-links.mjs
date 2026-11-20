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
