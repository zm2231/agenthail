// Every module an extension imports from its own folder is loaded fresh on each reload.
export async function resolve(specifier, context, next) {
  const resolved = await next(specifier, context);
  const parent = context.parentURL ? new URL(context.parentURL) : null;
  const generation = parent?.searchParams.get('agenthail-load');
  if (!generation || !resolved.url.startsWith('file:')) return resolved;
  const url = new URL(resolved.url);
  const root = parent.searchParams.get('agenthail-root');
  if (!root || !url.pathname.startsWith(root)) return resolved;
  url.searchParams.set('agenthail-load', generation);
  url.searchParams.set('agenthail-root', root);
  return { ...resolved, url: url.href };
}
