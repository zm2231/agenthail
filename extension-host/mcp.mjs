const PROTOCOL_VERSION = '2025-11-25';

export class CallError extends Error {
  constructor(refusal) {
    super(refusal.message ?? 'the call was refused');
    this.code = refusal.code ?? 'refused';
    this.retryable = Boolean(refusal.retryable);
    this.detail = refusal.detail;
  }
}

let nextID = 1;

function refusalFrom(status, text) {
  let refusal = { code: 'unavailable', message: `the daemon answered ${status}`, retryable: status >= 500 };
  try {
    const body = JSON.parse(text);
    if (body?.error?.code) refusal = { ...refusal, ...body.error };
  } catch {}
  return new CallError(refusal);
}

export async function callTool({ url, token }, name, args) {
  const response = await fetch(url, {
    method: 'POST',
    headers: {
      'content-type': 'application/json',
      accept: 'application/json, text/event-stream',
      authorization: `Bearer ${token}`,
      'mcp-protocol-version': PROTOCOL_VERSION,
    },
    body: JSON.stringify({ jsonrpc: '2.0', id: nextID++, method: 'tools/call', params: { name, arguments: args ?? {} } }),
  });
  const text = await response.text();
  if (!response.ok) throw refusalFrom(response.status, text);
  const body = JSON.parse(text);
  if (body.error) throw new CallError({ code: 'invalid', message: body.error.message });
  const result = body.result ?? {};
  if (result.isError) throw new CallError(result.structuredContent ?? { message: result.content?.[0]?.text });
  return result.structuredContent ?? {};
}

export async function fetchMount({ fetchURL, token }, mount, path, init = {}) {
  const headers = { ...(init.headers ?? {}) };
  let body = init.body;
  if (body !== undefined && typeof body !== 'string') {
    body = JSON.stringify(body);
    if (!Object.keys(headers).some(name => name.toLowerCase() === 'content-type')) headers['content-type'] = 'application/json';
  }
  const response = await fetch(fetchURL, {
    method: 'POST',
    headers: { 'content-type': 'application/json', authorization: `Bearer ${token}` },
    body: JSON.stringify({ mount, path, method: init.method ?? 'GET', headers, body: body ?? '' }),
  });
  const text = await response.text();
  if (!response.ok) throw refusalFrom(response.status, text);
  const reply = JSON.parse(text);
  const content = reply.bodyBase64 !== undefined ? Buffer.from(reply.bodyBase64, 'base64') : Buffer.from(reply.body ?? '', 'utf8');
  return {
    status: reply.status,
    ok: reply.status >= 200 && reply.status < 300,
    headers: reply.headers ?? {},
    text: () => content.toString('utf8'),
    json: () => JSON.parse(content.toString('utf8')),
    bytes: () => new Uint8Array(content),
  };
}

export async function setBadge({ badgeURL, token }, value, mount = '') {
  const response = await fetch(badgeURL, {
    method: 'POST',
    headers: { 'content-type': 'application/json', authorization: `Bearer ${token}` },
    body: JSON.stringify({ mount: mount ?? '', value: value ?? null }),
  });
  const text = await response.text();
  if (!response.ok) throw refusalFrom(response.status, text);
  return JSON.parse(text).badge;
}
