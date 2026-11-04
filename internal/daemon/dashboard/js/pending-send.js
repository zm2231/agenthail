import { isNetworkFailure, postAction } from "./actions.js";

const storagePrefix = "agenthail.pendingSend.";
const autoRetryWindow = 60 * 60 * 1000;
const abandonedAfter = 60 * 1000;

let handlers = { answered() {}, refused() {}, changed() {} };
const startedHere = new Set();
const pumping = new Set();

function readOne(storageKey) {
  try {
    return JSON.parse(globalThis.localStorage.getItem(storageKey));
  } catch {
    return null;
  }
}

function read() {
  const items = [];
  try {
    const storage = globalThis.localStorage;
    for (let index = 0; index < storage.length; index++) {
      const storageKey = storage.key(index);
      if (!storageKey?.startsWith(storagePrefix)) continue;
      const item = readOne(storageKey);
      if (item?.id) items.push(item);
    }
  } catch {
    return [];
  }
  return items.sort((a, b) => a.createdAt - b.createdAt || a.sequence - b.sequence || a.id.localeCompare(b.id));
}

function save(item) {
  try {
    globalThis.localStorage.setItem(storagePrefix + item.id, JSON.stringify(item));
    return true;
  } catch {
    return false;
  } finally {
    handlers.changed();
  }
}

function change(id, edit) {
  const item = readOne(storagePrefix + id);
  if (item) save(edit(item));
}

function take(id) {
  const item = readOne(storagePrefix + id);
  if (!item) return null;
  try {
    globalThis.localStorage.removeItem(storagePrefix + id);
  } catch {}
  handlers.changed();
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
    const item = readOne(storagePrefix + id);
    if (!item) return "answered";
    change(id, (current) => ({ ...current, attemptedAt: Date.now() }));
    try {
      const result = await postAction(item.wire, item.key);
      startedHere.delete(id);
      if (result?.status === "submitted") {
        change(id, (current) => ({ ...current, unconfirmed: true }));
        return "unconfirmed";
      }
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

let sequence = 0;

export function enqueueSend(sessionId, wire, text) {
  const id = crypto.randomUUID();
  const item = { id, key: crypto.randomUUID(), sessionId, wire, text, createdAt: Date.now(), sequence: ++sequence, attemptedAt: 0, unconfirmed: false };
  if (!save(item)) return false;
  startedHere.add(id);
  pump(sessionId);
  return true;
}

export function retryPendingSends() {
  return Promise.all([...new Set(read().map((item) => item.sessionId))].map(pump));
}

export async function retrySend(id) {
  const item = readOne(storagePrefix + id);
  if (!item) return;
  await attempt(id);
  await pump(item.sessionId);
}

export function takeBackSend(id) {
  return take(id);
}

export function keepSend(item) {
  save({ ...item, createdAt: 0, unconfirmed: true });
}

function showable(now) {
  return read()
    .filter((item) => !startedHere.has(item.id) && unanswered(item, now))
    .map((item) => ({ ...item, retrying: retriesByItself(item, now) }));
}

export function unconfirmedSends(sessionId, now = Date.now()) {
  return showable(now).filter((item) => item.sessionId === sessionId);
}

export function unconfirmedSendsOutside(known, now = Date.now()) {
  return showable(now).filter((item) => !known(item.sessionId));
}

export function movePendingSends(rekeyed) {
  for (const item of read()) {
    if (rekeyed[item.sessionId]) change(item.id, (current) => ({ ...current, sessionId: rekeyed[current.sessionId] || current.sessionId }));
  }
}

export function isPendingSendStorage(key) {
  return key === null || key?.startsWith(storagePrefix);
}
