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
    localStorage: { getItem: (key) => values.get(key) ?? null, setItem: (key, value) => values.set(key, value), removeItem: (key) => values.delete(key) },
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
  return { context, $, requests, answers, toasts, values };
}

async function loaded() {
  const tab = page();
  await dashboardModules(["composer"], tab.context);
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
  const [stored] = JSON.parse(tab.values.get("agenthail.pendingSends"));
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

console.log("dashboard composer send tests passed");
