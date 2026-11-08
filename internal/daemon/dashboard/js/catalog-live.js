import { app } from "./state.js";

const keep = 2000;
let recent = [];
let cursor = null;
let streamed = 0;
let fresh = false;

export function catalogStream() {
  return {
    ready: () => cursor !== null && fresh,
    path: () => `/api/v1/catalog-events?after=${Math.max(cursor, streamed)}`,
    seen: (event) => { streamed = Math.max(streamed, Number(event.id) || 0); },
    gap: () => { streamed = 0; fresh = false; },
  };
}

function held(id) {
  for (const listing of Object.values(app.sessionOrder || {})) if (listing.rows.has(id)) return listing.rows.get(id);
  return app.state.sessions.find((session) => session.id === id) || null;
}

function replace(row) {
  for (const listing of Object.values(app.sessionOrder || {})) if (listing.rows.has(row.id)) listing.rows.set(row.id, row);
  const index = app.state.sessions.findIndex((session) => session.id === row.id);
  if (index >= 0) app.state.sessions[index] = row;
  if (app.selected?.id !== row.id || index < 0) return false;
  app.selected = row;
  if (!app.history || JSON.stringify(app.history.capabilities) === JSON.stringify(row.capabilities)) return false;
  app.history.capabilities = row.capabilities;
  return true;
}

export function patch(envelope) {
  const { type, data = {} } = envelope;
  if (type === "session.upserted") {
    const row = data.session;
    if (!row) return { changed: false, news: true };
    const before = held(row.id);
    const controls = Boolean(before) && replace(row);
    return { changed: Boolean(before), controls, news: !before || before.placement !== row.placement };
  }
  if (type === "session.removed") return { changed: false, news: !data.sessionId || Boolean(held(data.sessionId)) };
  if (type === "session.queue") {
    const before = data.sessionId && held(data.sessionId);
    if (before) replace({ ...before, queueCount: data.queueCount });
    return { changed: Boolean(before), news: true };
  }
  return { changed: false, news: type.startsWith("delivery.") || type.startsWith("coordinator.") };
}

export function catalogEvent(event, { render, reread, controls: renderControls }) {
  const envelope = JSON.parse(event.data);
  if (envelope.stream !== "catalog") return;
  if (cursor !== null && envelope.seq <= cursor) return;
  recent.push(envelope);
  if (recent.length > keep) {
    recent = recent.slice(-keep);
    reread();
  }
  if (cursor === null) return;
  cursor = envelope.seq;
  const { changed, controls, news } = patch(envelope);
  if (controls) renderControls();
  if (changed) render();
  if (news) reread();
}

export function catalogRead(seq) {
  cursor = seq;
  fresh = true;
  recent = recent.filter((envelope) => envelope.seq > seq);
  for (const envelope of recent) {
    patch(envelope);
    cursor = Math.max(cursor, envelope.seq);
  }
}
