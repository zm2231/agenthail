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

const AGE_REFRESH_MS = 30_000;
const watched = new Map();

function ago(at, now) {
  const seconds = Math.max(0, Math.round((now - at) / 1000));
  if (seconds < 60) return `${seconds} s ago`;
  if (seconds < 3600) return `${Math.round(seconds / 60)} min ago`;
  if (seconds < 86400) return `${Math.round(seconds / 3600)} h ago`;
  return `${Math.round(seconds / 86400)} d ago`;
}

export function age(state, now = Date.now()) {
  if (!state || state.state === 'collecting') return 'collecting';
  if (state.state === 'failed') {
    const failed = `failed ${state.errorAt ? ago(state.errorAt, now) : ''}: ${state.error}`.replace(' :', ':');
    return state.at ? `${failed}, showing ${ago(state.at, now).replace(' ago', ' old')}` : failed;
  }
  return `updated ${ago(state.at, now)}`;
}

async function refetch(name) {
  const entry = watched.get(name);
  if (entry.inFlight) {
    entry.again = true;
    return;
  }
  entry.inFlight = true;
  try {
    const response = await fetch(`${mount}s?name=${encodeURIComponent(name)}`, { credentials: 'same-origin' });
    const body = await response.json().catch(() => ({}));
    if (response.ok) entry.state = body.source;
    else if (!entry.state || entry.state.state === 'collecting') {
      entry.state = { name, state: 'failed', error: body.error?.message ?? `the source answered ${response.status}` };
    }
  } catch (error) {
    if (!entry.state) entry.state = { name, state: 'failed', error: String(error?.message ?? error) };
  } finally {
    entry.inFlight = false;
  }
  draw(entry);
  if (entry.again) {
    entry.again = false;
    refetch(name);
  }
}

function draw(entry) {
  for (const render of entry.renders) {
    try {
      render(entry.state);
    } catch (error) {
      console.error(`rendering source ${entry.state?.name}:`, error);
    }
  }
}

// source calls render at once and again whenever the source is collected,
// so a page draws everything it can without waiting for any one source.
export function source(name, render) {
  if (!mount) throw new Error('source() works only on a page under /x/<extension>/');
  let entry = watched.get(name);
  if (!entry) {
    entry = { renders: [], state: { name, state: 'collecting' }, inFlight: false, again: false };
    watched.set(name, entry);
    refetch(name);
  }
  entry.renders.push(render);
  render(entry.state);
  listen();
}

export function sources(renders) {
  for (const [name, render] of Object.entries(renders)) source(name, render);
}

let events = null;

// changesAfter is the daemon's event cursor when it served this file, so a
// page hears only changes made after it loaded and never reloads for an
// older one. A page framed by the dashboard is reloaded by the dashboard.
function listen() {
  if (events || !id || typeof EventSource === 'undefined') return;
  events = new EventSource(`/api/v1/events?after=${changesAfter}`);
  const top = window.top === window;
  events.addEventListener('extension.changed', (event) => {
    const changed = JSON.parse(event.data).data?.extensions ?? [];
    if (top && changed.includes(id)) location.reload();
  });
  events.addEventListener('extension.source', (event) => {
    const data = JSON.parse(event.data).data ?? {};
    if (data.extension === id && watched.has(data.source)) refetch(data.source);
  });
  events.addEventListener('stream.reset', () => {
    if (top) location.reload();
    else for (const name of watched.keys()) refetch(name);
  });
  setInterval(() => {
    for (const entry of watched.values()) draw(entry);
  }, AGE_REFRESH_MS);
}

if (id && window.top === window) listen();
