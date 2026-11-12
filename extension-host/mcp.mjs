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
  if (!response.ok) {
    let refusal = { code: 'unavailable', message: `the daemon answered ${response.status}`, retryable: response.status >= 500 };
    try {
      const body = JSON.parse(text);
      if (body?.error?.code) refusal = { ...refusal, ...body.error };
    } catch {}
    throw new CallError(refusal);
  }
  const body = JSON.parse(text);
  if (body.error) throw new CallError({ code: 'invalid', message: body.error.message });
  const result = body.result ?? {};
  if (result.isError) throw new CallError(result.structuredContent ?? { message: result.content?.[0]?.text });
  return result.structuredContent ?? {};
}
