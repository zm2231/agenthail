import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";

const hour = 60 * 60 * 1000;
const storageKey = "agenthail.pendingSends";

function browserStorage() {
  const values = new Map();
  return {
    getItem: (key) => (values.has(key) ? values.get(key) : null),
    setItem: (key, value) => values.set(key, String(value)),
    removeItem: (key) => values.delete(key),
  };
}

function daemon() {
  const delivered = [];
  const seen = new Map();
  const state = { calls: [], answer: null };
  state.delivered = delivered;
  state.fetch = async (_url, request) => {
    const key = request.headers["Idempotency-Key"];
    state.calls.push({ key, body: request.body });
    await new Promise((resolve) => setTimeout(resolve, 1));
    if (state.answer) return state.answer();
    if (seen.has(key)) assert.equal(seen.get(key), request.body, "a retry must send the same body");
    else {
      seen.set(key, request.body);
      delivered.push(JSON.parse(request.body).message);
    }
    return { ok: true, json: async () => ({ ok: true }) };
  };
  return state;
}

let ids = 0;
async function tab(storage, server, locks) {
  const events = { answered: [], refused: [] };
  const context = {
    app: { selected: { id: "session-1" }, pendingIdempotency: new Map() },
    withHolder: (_id, payload) => payload,
    localStorage: storage,
    navigator: locks ? { locks } : {},
    crypto: { randomUUID: () => `id-${++ids}` },
    fetch: (...args) => server.fetch(...args),
  };
  await dashboardModules(["actions", "pending-send"], context);
  context.configurePendingSends({
    answered: (item) => events.answered.push(item.text),
    refused: (item, error) => events.refused.push([item.text, error.message]),
  });
  return { ...context, events };
}

const stored = (storage) => JSON.parse(storage.getItem(storageKey) || "[]");
const wire = (message, sessionId = "session-1") => ({ action: "send", sessionId, message, author: "person" });
const lost = () => { throw new TypeError("network lost"); };
const httpAnswer = (status, message) => () => ({ ok: false, status, text: async () => JSON.stringify({ error: { message } }) });
const age = (storage, by) => storage.setItem(storageKey, JSON.stringify(stored(storage).map((item) => ({ ...item, createdAt: item.createdAt - by, attemptedAt: item.attemptedAt - by }))));

{
  const storage = browserStorage();
  const server = daemon();
  const page = await tab(storage, server);
  let during;
  const original = server.fetch;
  server.fetch = (...args) => { during = stored(storage).length; return original(...args); };
  await page.enqueueSend("session-1", wire("hello"), "hello");
  assert.equal(during, 1, "a send is kept while it has no answer");
  assert.equal(storage.getItem(storageKey), null, "a confirmed send leaves nothing stored");
  assert.deepEqual(server.delivered, ["hello"]);
  assert.deepEqual(page.events.answered, ["hello"]);
}

{
  const storage = browserStorage();
  const server = daemon();
  const page = await tab(storage, server);
  server.answer = lost;
  await page.enqueueSend("session-1", wire("lost"), "lost");
  assert.equal(stored(storage)[0].unconfirmed, true, "no answer leaves it unconfirmed");
  assert.equal(page.unconfirmedSends("session-1")[0].retrying, true);
  assert.equal(page.events.refused.length, 0, "no answer does not return the text");
  server.answer = null;
  await page.retryPendingSends();
  assert.equal(server.calls[0].key, server.calls[1].key, "a retry keeps its key");
  assert.equal(server.calls[0].body, server.calls[1].body, "a retry keeps its body");
  assert.equal(storage.getItem(storageKey), null);
}

{
  const storage = browserStorage();
  const server = daemon();
  const page = await tab(storage, server);
  server.answer = httpAnswer(409, "This session has ended.");
  await page.enqueueSend("session-1", wire("refused"), "refused");
  assert.deepEqual(page.events.refused, [["refused", "This session has ended."]]);
  assert.equal(storage.getItem(storageKey), null, "a refusal is not retried");
  server.answer = null;
  await page.enqueueSend("session-1", wire("refused"), "refused");
  assert.notEqual(server.calls[0].key, server.calls[1].key, "sending again after a refusal uses a new key");
}

{
  const storage = browserStorage();
  const server = daemon();
  const page = await tab(storage, server);
  server.answer = httpAnswer(401, "dashboard access token required");
  await page.enqueueSend("session-1", wire("signed out"), "signed out");
  assert.equal(page.events.refused.length, 0, "a lost sign-in is not a refusal");
  assert.equal(stored(storage).length, 1, "a lost sign-in keeps the send");
  assert.equal(stored(storage)[0].unconfirmed, true);
  server.answer = null;
  await page.retryPendingSends();
  assert.deepEqual(server.delivered, ["signed out"], "it retries once the sign-in works, within the hour");
}

{
  const storage = browserStorage();
  const server = daemon();
  const page = await tab(storage, server);
  server.answer = httpAnswer(401, "dashboard access token required");
  await page.enqueueSend("session-1", wire("late"), "late");
  server.answer = null;
  age(storage, hour + 1);
  const calls = server.calls.length;
  await page.retryPendingSends();
  assert.equal(server.calls.length, calls, "past the hour nothing retries by itself");
  const [item] = page.unconfirmedSends("session-1");
  assert.equal(item.retrying, false);
  await page.retrySend(item.id);
  assert.deepEqual(server.delivered, ["late"], "a tap retries it");
  assert.equal(server.calls[0].key, server.calls.at(-1).key);
}

{
  const storage = browserStorage();
  const server = daemon();
  const page = await tab(storage, server);
  server.answer = lost;
  await page.enqueueSend("session-1", wire("first"), "first");
  age(storage, hour + 1);
  server.answer = null;
  await page.enqueueSend("session-1", wire("second"), "second");
  assert.deepEqual(server.delivered, ["second"], "an expired send does not hold up a new one");
  assert.equal(page.unconfirmedSends("session-1").length, 1);
  assert.equal(page.takeBackSend(page.unconfirmedSends("session-1")[0].id).text, "first", "put back hands over the text");
  assert.equal(storage.getItem(storageKey), null);
}

{
  const storage = browserStorage();
  const server = daemon();
  const page = await tab(storage, server);
  server.answer = lost;
  await page.enqueueSend("session-1", wire("one"), "one");
  await page.enqueueSend("session-1", wire("two"), "two");
  assert.deepEqual(stored(storage).map((item) => item.text), ["one", "two"]);
  server.answer = null;
  await page.retryPendingSends();
  assert.deepEqual(server.delivered, ["one", "two"], "sends to a session go in order");
}

{
  const storage = browserStorage();
  const server = daemon();
  const page = await tab(storage, server);
  server.answer = lost;
  await page.enqueueSend("old", wire("moved", "old"), "moved");
  page.movePendingSends({ old: "new" });
  assert.equal(page.unconfirmedSends("new").length, 1, "a rekeyed session keeps its send");
  assert.equal(JSON.parse(storage.getItem(storageKey))[0].wire.sessionId, "old", "the request stays the one the key was used for");
}

for (const useLocks of [false, true]) {
  const storage = browserStorage();
  const server = daemon();
  const held = new Set();
  const locks = useLocks ? {
    request: async (name, _options, work) => {
      if (held.has(name)) return work(null);
      held.add(name);
      try { return await work({ name }); } finally { held.delete(name); }
    },
  } : undefined;
  const one = await tab(storage, server, locks);
  const two = await tab(storage, server, locks);
  server.answer = lost;
  await one.enqueueSend("session-1", wire("both"), "both");
  server.answer = null;
  await Promise.all([one.retryPendingSends(), two.retryPendingSends()]);
  assert.deepEqual(server.delivered, ["both"], `two tabs deliver once (locks: ${useLocks})`);
  assert.equal(storage.getItem(storageKey), null);
  assert.equal(one.events.answered.length + two.events.answered.length, 1, "one tab reports it");
  if (useLocks) assert.equal(server.calls.length, 2, "with locks only one tab retries");
}

{
  const storage = browserStorage();
  const server = daemon();
  const one = await tab(storage, server);
  const two = await tab(storage, server);
  let release;
  const original = server.fetch;
  server.fetch = async (...args) => { await new Promise((resolve) => { release = resolve; }); return original(...args); };
  const sending = one.enqueueSend("session-1", wire("in flight"), "in flight");
  await new Promise((resolve) => setTimeout(resolve, 1));
  server.fetch = original;
  await two.retryPendingSends();
  assert.equal(server.calls.length, 0, "another tab leaves a send that is still in flight alone");
  assert.equal(two.unconfirmedSends("session-1").length, 0, "and does not call it unconfirmed");
  release();
  await sending;
  assert.deepEqual(server.delivered, ["in flight"]);
}

{
  const storage = browserStorage();
  const server = daemon();
  const page = await tab(storage, server);
  storage.setItem(storageKey, JSON.stringify([{ id: "closed", key: "k", sessionId: "session-1", wire: wire("tab closed"), text: "tab closed", createdAt: Date.now() - 120000, attemptedAt: Date.now() - 120000, unconfirmed: false }]));
  await page.retryPendingSends();
  assert.deepEqual(server.delivered, ["tab closed"], "a send whose tab closed mid-request is retried");
}

console.log("dashboard pending send tests passed");
