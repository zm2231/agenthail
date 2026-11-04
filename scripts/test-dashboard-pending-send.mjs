import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";

const hour = 60 * 60 * 1000;

function browserStorage() {
  const values = new Map();
  const storage = {
    get length() { return values.size; },
    key: (index) => [...values.keys()][index] ?? null,
    getItem: (key) => (values.has(key) ? values.get(key) : null),
    setItem: (key, value) => {
      storage.beforeSet?.(key);
      values.set(key, String(value));
    },
    removeItem: (key) => values.delete(key),
  };
  return storage;
}

function stored(storage) {
  const items = [];
  for (let index = 0; index < storage.length; index++) items.push(JSON.parse(storage.getItem(storage.key(index))));
  return items.sort((a, b) => a.createdAt - b.createdAt || a.sequence - b.sequence);
}

function daemon() {
  const seen = new Map();
  const state = { calls: [], answer: null, delivered: [] };
  state.fetch = async (_url, request) => {
    const key = request.headers["Idempotency-Key"];
    state.calls.push({ key, body: request.body });
    await new Promise((resolve) => setTimeout(resolve, 1));
    if (state.answer) return state.answer();
    if (seen.has(key)) assert.equal(seen.get(key), request.body, "a retry must send the same body");
    else {
      seen.set(key, request.body);
      state.delivered.push(JSON.parse(request.body).message);
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

const settled = () => new Promise((resolve) => setTimeout(resolve, 15));
const wire = (message, sessionId = "session-1") => ({ action: "send", sessionId, message, author: "person" });
const lost = () => { throw new TypeError("network lost"); };
const httpAnswer = (status, message) => () => ({ ok: false, status, text: async () => JSON.stringify({ error: { message } }) });
const submitted = () => ({ ok: true, json: async () => ({ ok: true, status: "submitted" }) });
const send = async (page, message, sessionId) => {
  assert.equal(page.enqueueSend(sessionId || "session-1", wire(message, sessionId), message), true);
  await settled();
};
const age = (storage, by) => {
  for (const item of stored(storage)) storage.setItem(`agenthail.pendingSend.${item.id}`, JSON.stringify({ ...item, createdAt: item.createdAt - by, attemptedAt: item.attemptedAt && item.attemptedAt - by }));
};

{
  const storage = browserStorage();
  const server = daemon();
  const page = await tab(storage, server);
  let during;
  const original = server.fetch;
  server.fetch = (...args) => { during = storage.length; return original(...args); };
  await send(page, "hello");
  assert.equal(during, 1, "a send is kept while it has no answer");
  assert.equal(storage.length, 0, "a confirmed send leaves nothing stored");
  assert.deepEqual(server.delivered, ["hello"]);
  assert.deepEqual(page.events.answered, ["hello"]);
}

{
  const storage = browserStorage();
  const server = daemon();
  const page = await tab(storage, server);
  server.answer = lost;
  await send(page, "lost");
  assert.equal(stored(storage)[0].unconfirmed, true, "no answer leaves it unconfirmed");
  assert.equal(page.unconfirmedSends("session-1")[0].retrying, true);
  assert.equal(page.events.refused.length, 0, "no answer does not return the text");
  server.answer = null;
  await page.retryPendingSends();
  assert.equal(server.calls[0].key, server.calls[1].key, "a retry keeps its key");
  assert.equal(server.calls[0].body, server.calls[1].body, "a retry keeps its body");
  assert.equal(storage.length, 0);
}

{
  const storage = browserStorage();
  const server = daemon();
  const page = await tab(storage, server);
  server.answer = httpAnswer(409, "This session has ended.");
  await send(page, "refused");
  assert.deepEqual(page.events.refused, [["refused", "This session has ended."]]);
  assert.equal(storage.length, 0, "a refusal is not retried");
  server.answer = null;
  await send(page, "refused");
  assert.notEqual(server.calls[0].key, server.calls[1].key, "sending again after a refusal uses a new key");
}

{
  const storage = browserStorage();
  const server = daemon();
  const page = await tab(storage, server);
  server.answer = httpAnswer(401, "dashboard access token required");
  await send(page, "signed out");
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
  await send(page, "late");
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
  await send(page, "first");
  age(storage, hour + 1);
  server.answer = null;
  await send(page, "second");
  assert.deepEqual(server.delivered, ["second"], "an expired send does not hold up a new one");
  assert.equal(page.unconfirmedSends("session-1").length, 1);
  assert.equal(page.takeBackSend(page.unconfirmedSends("session-1")[0].id).text, "first", "put back hands over the text");
  assert.equal(storage.length, 0);
}

{
  const storage = browserStorage();
  const server = daemon();
  const page = await tab(storage, server);
  server.answer = lost;
  await send(page, "one");
  page.enqueueSend("session-1", wire("two"), "two");
  await settled();
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
  await send(page, "moved", "old");
  page.movePendingSends({ old: "new" });
  assert.equal(page.unconfirmedSends("new").length, 1, "a rekeyed session keeps its send");
  assert.equal(stored(storage)[0].wire.sessionId, "old", "the request stays the one the key was used for");
}

{
  const storage = browserStorage();
  const server = daemon();
  const page = await tab(storage, server);
  server.answer = lost;
  await send(page, "gone", "removed");
  await send(page, "here");
  const known = (id) => id === "session-1";
  assert.deepEqual([...page.unconfirmedSendsOutside(known)].map((item) => item.text), ["gone"], "a send for an unlisted session can still be found");
}

{
  const storage = browserStorage();
  const server = daemon();
  const page = await tab(storage, server);
  server.answer = submitted;
  await send(page, "still pending");
  assert.equal(stored(storage).length, 1, "a 202 submitted is not an answer, so the send stays");
  assert.equal(page.events.answered.length, 0, "and nothing says it arrived");
  assert.equal(page.unconfirmedSends("session-1").length, 1);
  server.answer = null;
  await page.retryPendingSends();
  assert.equal(storage.length, 0, "the completed receipt removes it");
  assert.deepEqual(page.events.answered, ["still pending"]);
}

{
  const storage = browserStorage();
  const server = daemon();
  server.answer = lost;
  const one = await tab(storage, server);
  const two = await tab(storage, server);
  storage.beforeSet = () => {
    storage.beforeSet = undefined;
    two.enqueueSend("session-1", wire("from two"), "from two");
  };
  one.enqueueSend("session-1", wire("from one"), "from one");
  await settled();
  assert.deepEqual(stored(storage).map((item) => item.text).sort(), ["from one", "from two"], "a write in one tab during another's keeps both sends");

  const [first, second] = stored(storage);
  storage.beforeSet = () => {
    storage.beforeSet = undefined;
    one.takeBackSend(second.id);
  };
  two.movePendingSends({ "session-1": "session-2" });
  assert.ok(stored(storage).some((item) => item.id === first.id), "a change in one tab leaves another tab's send alone");
}

{
  const storage = browserStorage();
  const server = daemon();
  const page = await tab(storage, server);
  storage.setItem = () => { throw new Error("quota"); };
  assert.equal(page.enqueueSend("session-1", wire("no room"), "no room"), false, "a send that can't be stored says so");
  await settled();
  assert.equal(server.calls.length, 0, "and is not sent by the store");
}

{
  const storage = browserStorage();
  const server = daemon();
  server.answer = lost;
  const one = await tab(storage, server);
  const two = await tab(storage, server);
  const now = Date.now;
  const tied = { sessionId: "session-1", createdAt: 1000, sequence: 1, attemptedAt: 1000, unconfirmed: true };
  storage.setItem("agenthail.pendingSend.b", JSON.stringify({ ...tied, id: "b", key: "b", text: "b", wire: wire("b") }));
  storage.setItem("agenthail.pendingSend.a", JSON.stringify({ ...tied, id: "a", key: "a", text: "a", wire: wire("a") }));
  assert.deepEqual([...one.unconfirmedSends("session-1", now())].map((item) => item.id), ["a", "b"], "sends tied across tabs have one order");
  assert.deepEqual([...two.unconfirmedSends("session-1", now())].map((item) => item.id), ["a", "b"]);
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
  await send(one, "both");
  server.answer = null;
  await Promise.all([one.retryPendingSends(), two.retryPendingSends()]);
  assert.deepEqual(server.delivered, ["both"], `two tabs deliver once (locks: ${useLocks})`);
  assert.equal(storage.length, 0);
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
  one.enqueueSend("session-1", wire("in flight"), "in flight");
  await settled();
  server.fetch = original;
  await two.retryPendingSends();
  assert.equal(server.calls.length, 0, "another tab leaves a send that is still in flight alone");
  assert.equal(two.unconfirmedSends("session-1").length, 0, "and does not call it unconfirmed");
  release();
  await settled();
  assert.deepEqual(server.delivered, ["in flight"]);
}

{
  const storage = browserStorage();
  const server = daemon();
  const page = await tab(storage, server);
  const closed = { id: "closed", key: "k", sessionId: "session-1", wire: wire("tab closed"), text: "tab closed", createdAt: Date.now() - 120000, sequence: 1, attemptedAt: Date.now() - 120000, unconfirmed: false };
  storage.setItem("agenthail.pendingSend.closed", JSON.stringify(closed));
  storage.setItem("agenthail.pendingSend.broken", "{not json");
  storage.setItem("agenthail.other", "kept");
  assert.equal(page.unconfirmedSends("session-1").length, 1, "an unreadable entry is skipped");
  await page.retryPendingSends();
  assert.deepEqual(server.delivered, ["tab closed"], "a send whose tab closed mid-request is retried");
  assert.equal(storage.getItem("agenthail.other"), "kept");
}

console.log("dashboard pending send tests passed");
