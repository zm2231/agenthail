import { isNetworkFailure, postAction } from "./actions.js";

const storageKey = "agenthail.pendingSends";
const autoRetryWindow = 60 * 60 * 1000;
const abandonedAfter = 60 * 1000;

let unstored = [];
let handlers = { answered() {}, refused() {}, changed() {} };
const startedHere = new Set();
const pumping = new Set();

function read() {
  try {
    const stored = globalThis.localStorage?.getItem(storageKey);
    return stored ? [...JSON.parse(stored), ...unstored] : [...unstored];
  } catch {
    return [...unstored];
  }
}

function write(items) {
  try {
    if (!globalThis.localStorage) throw Error("no storage");
    if (items.length) globalThis.localStorage.setItem(storageKey, JSON.stringify(items));
    else globalThis.localStorage.removeItem(storageKey);
    unstored = [];
  } catch {
    unstored = items;
  }
  handlers.changed();
}

function change(id, edit) {
  write(read().map((item) => (item.id === id ? edit(item) : item)));
}

function take(id) {
  const items = read();
  const item = items.find((candidate) => candidate.id === id);
  if (item) write(items.filter((candidate) => candidate.id !== id));
  return item;
}

function unanswered(item, now) {
  return item.unconfirmed || now - (item.attemptedAt || item.createdAt) > abandonedAfter;
}

function retriesByItself(item, now) {
  return now - item.createdAt < autoRetryWindow;
}

function nextFor(sessionId, now) {
  return read().find((item) => item.sessionId === sessionId && (startedHere.has(item.id) || (unanswered(item, now) && retriesByItself(item, now))));
}

export function sendOutcome(error) {
  return isNetworkFailure(error) || error?.unknown || error?.status === 401 ? "unconfirmed" : "refused";
}

function holdingItem(id, work) {
  const locks = globalThis.navigator?.locks;
  if (!locks) return work();
  return locks.request(`agenthail-pending-send-${id}`, { ifAvailable: true }, (lock) => (lock ? work() : "elsewhere"));
}

function attempt(id) {
  return holdingItem(id, async () => {
    const item = read().find((candidate) => candidate.id === id);
    if (!item) return "answered";
    change(id, (current) => ({ ...current, attemptedAt: Date.now() }));
    try {
      const result = await postAction(item.wire, item.key);
      startedHere.delete(id);
      if (take(id)) handlers.answered(item, result);
      return "answered";
    } catch (error) {
      startedHere.delete(id);
      if (sendOutcome(error) === "refused") {
        if (take(id)) handlers.refused(item, error);
        return "refused";
      }
      change(id, (current) => ({ ...current, unconfirmed: true }));
      return "unconfirmed";
    }
  });
}

async function pump(sessionId) {
  if (pumping.has(sessionId)) return;
  pumping.add(sessionId);
  try {
    for (;;) {
      const item = nextFor(sessionId, Date.now());
      if (!item) return;
      const outcome = await attempt(item.id);
      if (outcome === "unconfirmed" || outcome === "elsewhere") return;
    }
  } finally {
    pumping.delete(sessionId);
  }
}

export function configurePendingSends(next) {
  handlers = { ...handlers, ...next };
}

export function enqueueSend(sessionId, wire, text) {
  const id = crypto.randomUUID();
  write([...read(), { id, key: crypto.randomUUID(), sessionId, wire, text, createdAt: Date.now(), attemptedAt: 0, unconfirmed: false }]);
  startedHere.add(id);
  return pump(sessionId);
}

export function retryPendingSends() {
  return Promise.all([...new Set(read().map((item) => item.sessionId))].map(pump));
}

export async function retrySend(id) {
  const item = read().find((candidate) => candidate.id === id);
  if (!item) return;
  await attempt(id);
  await pump(item.sessionId);
}

export function takeBackSend(id) {
  return take(id);
}

export function unconfirmedSends(sessionId, now = Date.now()) {
  return read()
    .filter((item) => item.sessionId === sessionId && !startedHere.has(item.id) && unanswered(item, now))
    .map((item) => ({ ...item, retrying: retriesByItself(item, now) }));
}

export function movePendingSends(rekeyed) {
  const items = read();
  if (items.some((item) => rekeyed[item.sessionId])) write(items.map((item) => ({ ...item, sessionId: rekeyed[item.sessionId] || item.sessionId })));
}

export function isPendingSendStorage(key) {
  return key === storageKey;
}
