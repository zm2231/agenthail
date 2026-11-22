export const REQUEST_HEADER = "x-agenthail-guard-request";

function tagFetches(header) {
  const original = window.fetch.bind(window);
  const tag = Math.random().toString(36).slice(2, 8);
  let next = 0;
  window.fetch = (input, init = {}) => {
    const headers = new Headers(init.headers || (input instanceof Request ? input.headers : undefined));
    headers.set(header, `${tag}-${++next}`);
    return original(input, { ...init, headers });
  };
}

// Network events come from the browser, so they still report a page whose script stopped answering.
export async function trackRequests(page) {
  const pending = new Map();
  await page.evaluateOnNewDocument(tagFetches, REQUEST_HEADER);
  page.on("request", (request) => {
    const id = request.headers()[REQUEST_HEADER];
    if (id) pending.set(request, { id, path: new URL(request.url()).pathname + new URL(request.url()).search, sent: Date.now(), status: null });
  });
  page.on("response", (response) => {
    const entry = pending.get(response.request());
    if (!entry) return;
    entry.status = response.status();
    entry.streaming = (response.headers()["content-type"] || "").startsWith("text/event-stream");
  });
  // A navigation ends the old document's requests without a finished or failed event.
  page.on("framenavigated", (frame) => {
    if (frame === page.mainFrame()) pending.clear();
  });
  page.on("requestfinished", (request) => pending.delete(request));
  page.on("requestfailed", (request) => pending.delete(request));
  return () => [...pending.values()].sort((a, b) => a.sent - b.sent);
}

export function describeRequests(entries) {
  if (!entries.length) return "no tagged requests pending";
  const now = Date.now();
  return entries.map((entry) => `${entry.id} ${entry.path} ${entry.status === null ? "no response" : entry.streaming ? `streaming ${entry.status}` : `response ${entry.status}, body unfinished`} after ${((now - entry.sent) / 1000).toFixed(0)} s`).join("; ");
}
