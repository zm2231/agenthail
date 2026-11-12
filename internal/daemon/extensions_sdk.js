// Imported by extension pages as /x/_ah.js. Calls go to the extension that serves the page.
const mount = location.pathname.match(/^\/x\/[^/]+\//)?.[0];

export const id = mount ? mount.slice(3, -1) : '';

export async function action(name, input = {}) {
  if (!mount) throw new Error('action() works only on a page under /x/<extension>/');
  const response = await fetch(`${mount}a/${encodeURIComponent(name)}`, {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(input),
  });
  const body = await response.json().catch(() => ({}));
  if (!response.ok) {
    const error = new Error(body.error?.message ?? `the action answered ${response.status}`);
    error.code = body.error?.code ?? 'failed';
    throw error;
  }
  return body.result;
}
