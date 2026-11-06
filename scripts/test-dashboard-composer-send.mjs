import "./test-home.mjs";
import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";

function element() {
  return {
    value: "", hidden: false, disabled: false, innerHTML: "", textContent: "", dataset: {}, style: {},
    classList: { toggle() {} },
    addEventListener() {},
    querySelector: () => ({ textContent: "" }),
  };
}

function page() {
  const elements = new Map();
  const $ = (selector) => {
    if (!elements.has(selector)) elements.set(selector, element());
    return elements.get(selector);
  };
  const values = new Map();
  const requests = [];
  const answers = [];
  const toasts = [];
  let ids = 0;
  const tab = { storageFull: false };
  const context = {
    $,
    app: {
      selected: { id: "a", alias: "a", status: "idle" },
      state: { sessions: [{ id: "a", alias: "a" }, { id: "b", alias: "b" }] },
      history: { capabilities: { steer: true } },
      drafts: new Map(),
      pendingIdempotency: new Map(),
      slashCommands: [],
    },
    localStorage: {
      get length() { return values.size; },
      key: (index) => [...values.keys()][index] ?? null,
      getItem: (key) => values.get(key) ?? null,
      setItem: (key, value) => {
        if (tab.storageFull) throw new Error("quota");
        values.set(key, value);
      },
      removeItem: (key) => values.delete(key),
    },
    navigator: {},
    window: { addEventListener() {}, confirm: () => true },
    crypto: { randomUUID: () => `id-${++ids}` },
    fetch: async (_url, request) => {
      requests.push({ key: request.headers["Idempotency-Key"], body: JSON.parse(request.body) });
      const answer = answers.shift();
      if (answer) return answer();
      return { ok: true, json: async () => ({ ok: true }) };
    },
    toast: (text) => toasts.push(text),
    load: async () => {},
    selectSession: async () => {},
    renderHolderPicker() {},
    chosenHolder: () => undefined,
    sharedHolders: () => [],
    withHolder: (_id, payload) => payload,
    deliveryStatusLabel: () => "Sent",
    escape: (text) => text,
    rememberToolRow() {},
  };
  return Object.assign(tab, { context, $, requests, answers, toasts, values });
}

async function loaded() {
  const tab = page();
  await dashboardModules(["composer", "send-status", "pending-send"], tab.context);
  tab.context.bindComposerEvents();
  return tab;
}

const settled = () => new Promise((resolve) => setTimeout(resolve, 5));
const refused = (message) => () => ({ ok: false, status: 409, text: async () => JSON.stringify({ error: { message } }) });
const lost = () => { throw new TypeError("network lost"); };

{
  const tab = await loaded();
  tab.$("#message").value = "first";
  tab.$("#send").dataset.mode = "send";
  const sending = tab.context.send();
  assert.equal(tab.$("#message").value, "", "the text leaves the composer at once");
  tab.$("#message").value = "typed during the request";
  await sending;
  await settled();
  assert.equal(tab.$("#message").value, "typed during the request", "an answer leaves newer text alone");
  assert.equal(tab.requests[0].body.message, "first");
}

{
  const tab = await loaded();
  tab.$("#message").value = "for a";
  tab.$("#send").dataset.mode = "send";
  tab.answers.push(refused("Not now."));
  const sending = tab.context.send();
  tab.context.app.selected = { id: "b", alias: "b", status: "idle" };
  tab.$("#message").value = "draft for b";
  tab.context.app.drafts.set("a", "");
  await sending;
  await settled();
  assert.equal(tab.$("#message").value, "draft for b", "another session's composer is left alone");
  assert.equal(tab.context.app.drafts.get("a"), "for a", "a refusal goes back to the session it was for");
  assert.equal(tab.requests[0].body.sessionId, "a");
}

{
  const tab = await loaded();
  tab.$("#message").value = "refused";
  tab.$("#send").dataset.mode = "send";
  tab.answers.push(refused("This session has ended."));
  const sending = tab.context.send();
  tab.$("#message").value = "newer";
  await sending;
  await settled();
  assert.equal(tab.$("#message").value, "refused\n\nnewer", "a refused message returns above newer text");
  assert.ok(tab.toasts.includes("This session has ended."), "and says why");
}

{
  const tab = await loaded();
  tab.$("#message").value = "maybe arrived";
  tab.$("#send").dataset.mode = "send";
  tab.answers.push(lost);
  await tab.context.send();
  await settled();
  assert.equal(tab.$("#message").value, "", "no answer keeps it out of the composer");
  assert.equal(tab.$("#send-status").hidden, false, "and shows the row");
  assert.match(tab.$("#send-status-text").textContent, /^Not confirmed yet · retrying “maybe arrived”$/);
}

for (const [typed, mode, expected] of [["redirect", "steer", "redirect"], ["/steer redirect", "send", "redirect"]]) {
  const tab = await loaded();
  tab.$("#message").value = typed;
  tab.$("#send").dataset.mode = "send";
  tab.answers.push(lost);
  await tab.context.send(mode);
  await settled();
  assert.equal(tab.requests[0].body.action, "steer");
  assert.equal(tab.requests[0].body.message, expected);
  const key = tab.requests[0].key;
  const stored = JSON.parse([...tab.values.values()][0]);
  assert.equal(stored.key, key, "a lost steer keeps its key to retry");
  assert.equal(stored.text, typed, "and the text as typed");
}

{
  const tab = await loaded();
  tab.$("#message").value = "/compact";
  tab.$("#send").dataset.mode = "send";
  const sending = tab.context.send();
  assert.equal(tab.$("#message").value, "/compact", "a command stays until it is answered");
  await sending;
  assert.equal(tab.$("#message").value, "");
  assert.equal(tab.values.size, 0, "a command is not stored");

  tab.$("#message").value = "/compact";
  const typedOver = tab.context.send();
  tab.$("#message").value = "something else";
  await typedOver;
  assert.equal(tab.$("#message").value, "something else", "an answered command leaves newer text alone");
}

{
  const tab = await loaded();
  tab.storageFull = true;
  tab.$("#message").value = "no room";
  tab.$("#send").dataset.mode = "send";
  tab.answers.push(lost);
  await tab.context.send();
  await settled();
  assert.equal(tab.$("#message").value, "no room", "with nowhere to store it, the text stays in the composer");
  assert.equal(tab.requests.length, 1);
  await tab.context.send();
  await settled();
  assert.equal(tab.requests[0].key, tab.requests[1].key, "sending it again keeps the key");
  assert.equal(tab.$("#message").value, "", "and an answer clears it");
}

{
  const tab = await loaded();
  tab.$("#message").value = "for a removed session";
  tab.$("#send").dataset.mode = "send";
  tab.answers.push(lost);
  await tab.context.send();
  await settled();
  tab.context.app.state.sessions = [{ id: "b", alias: "b" }];
  tab.context.app.selected = { id: "b", alias: "b", status: "idle" };
  tab.$("#message").value = "";
  tab.context.renderSendStatus();
  assert.equal(tab.$("#send-status").hidden, false, "a send for an unlisted session still shows");
  assert.match(tab.$("#send-status-text").textContent, /no longer listed/);
  const [stored] = [...tab.values.values()].map((value) => JSON.parse(value));
  tab.context.returnToComposer(tab.context.takeBackSend(stored.id));
  assert.equal(tab.$("#message").value, "for a removed session", "and goes back to a composer that can be reached");
}

{
  const tab = await loaded();
  tab.context.app.codexSearch = { results: [{ id: "searched", alias: "searched" }] };
  tab.context.app.selected = { id: "searched", alias: "searched", status: "idle" };
  tab.$("#message").value = "for a search result";
  tab.$("#send").dataset.mode = "send";
  tab.answers.push(refused("Not now."));
  const sending = tab.context.send();
  tab.context.app.selected = { id: "b", alias: "b", status: "idle" };
  tab.$("#message").value = "";
  await sending;
  await settled();
  assert.equal(tab.$("#message").value, "", "a refusal for a search result stays out of another session's composer");
  assert.equal(tab.context.app.drafts.get("searched"), "for a search result", "and goes to its own draft");
}

{
  const tab = await loaded();
  tab.$("#message").value = "nothing selected";
  tab.$("#send").dataset.mode = "send";
  tab.answers.push(lost);
  await tab.context.send();
  await settled();
  tab.context.app.state.sessions = [];
  tab.context.app.selected = null;
  tab.context.renderSendStatus();
  assert.ok(tab.toasts.some((text) => /Open any conversation/.test(text)), "with nothing selected, an unlisted send is pointed out");
  assert.equal(tab.$("#send-status").hidden, true);

  const [stored] = [...tab.values.values()].map((value) => JSON.parse(value));
  tab.context.returnToComposer(tab.context.takeBackSend(stored.id));
  assert.equal(tab.values.size, 1, "with nowhere to put a refused send, it stays stored");
  tab.context.app.selected = { id: "b", alias: "b", status: "idle" };
  tab.context.renderSendStatus();
  assert.equal(tab.$("#send-status").hidden, false, "and shows once a conversation is open");
  assert.match(tab.$("#send-status-text").textContent, /^Not confirmed\. It may already have arrived/, "without retrying by itself");
}

{
  const tab = await loaded();
  tab.storageFull = true;
  tab.$("#message").value = "no room, signed out";
  tab.$("#send").dataset.mode = "send";
  const unauthorized = () => ({ ok: false, status: 401, text: async () => JSON.stringify({ error: { message: "dashboard access token required" } }) });
  tab.answers.push(unauthorized, unauthorized);
  await tab.context.send();
  await tab.context.send();
  assert.equal(tab.requests[0].key, tab.requests[1].key, "with no storage, a 401 keeps the key too");
  assert.equal(tab.$("#message").value, "no room, signed out");
}

console.log("dashboard composer send tests passed");
