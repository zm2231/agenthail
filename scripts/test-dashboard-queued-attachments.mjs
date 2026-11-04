import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";

function element() {
  const listeners = {};
  return {
    value: "", hidden: false, disabled: false, innerHTML: "", textContent: "", dataset: {}, style: {},
    classList: { toggle() {} },
    listeners,
    addEventListener(type, listener) { listeners[type] = listener; },
    querySelector: () => ({ textContent: "" }),
  };
}

async function loaded({ status = "busy", values = new Map(), storageFull = false } = {}) {
  const elements = new Map();
  const $ = (selector) => {
    if (!elements.has(selector)) elements.set(selector, element());
    return elements.get(selector);
  };
  const requests = [];
  const answers = [];
  const toasts = [];
  let ids = 0;
  const context = {
    $,
    app: {
      selected: { id: "a", alias: "a", status },
      state: { sessions: [{ id: "a", alias: "a" }, { id: "b", alias: "b" }] },
      history: { capabilities: { steer: true, interrupt: true } },
      drafts: new Map(),
      pendingIdempotency: new Map(),
      slashCommands: [],
      rekeyed: {},
      voice: {},
    },
    localStorage: {
      get length() { return values.size; },
      key: (index) => [...values.keys()][index] ?? null,
      getItem: (key) => values.get(key) ?? null,
      setItem: (key, value) => {
        if (storageFull) throw new Error("quota");
        values.set(key, value);
      },
      removeItem: (key) => values.delete(key),
    },
    navigator: {},
    window: { addEventListener() {}, confirm: () => true },
    crypto: { randomUUID: () => `id-${++ids}` },
    fetch: async (_url, request) => {
      requests.push(JSON.parse(request.body));
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
    escape: (text) => String(text),
    rememberToolRow() {},
  };
  await dashboardModules(["composer", "send-status", "pending-send", "composer-attachments", "queue-take-back", "rekeys"], context);
  context.bindComposerEvents();
  return { context, $, requests, answers, toasts, values };
}

const settled = () => new Promise((resolve) => setTimeout(resolve, 5));
const answered = (body) => () => ({ ok: true, json: async () => body });
const refused = (message) => () => ({ ok: false, status: 409, text: async () => JSON.stringify({ error: { message } }) });
const lost = () => { throw new TypeError("network lost"); };
const shot = { id: "upload:aa/shot.png", name: "shot.png", mediaType: "image/png", bytes: 138 };
const gone = { id: "upload:bb/old.png", name: "old.png", mediaType: "image/png", bytes: 90, missing: true };
const canceled = answered({ ok: true, result: { message: "look at this", attachments: [shot, gone] } });

async function takenBack(tab) {
  tab.answers.push(canceled);
  const missing = await tab.context.takeBackQueued(7, "a");
  assert.deepEqual(tab.requests.at(-1), { action: "queue-cancel", sessionId: "a", queueId: 7 });
  return missing;
}

{
  const tab = await loaded();
  const missing = await takenBack(tab);
  assert.equal(tab.$("#message").value, "look at this", "take back puts the text in the composer");
  assert.equal(tab.$("#composer-attachments").hidden, false, "and shows its attachments");
  assert.match(tab.$("#composer-attachments").innerHTML, /shot\.png/);
  assert.match(tab.$("#composer-attachments").innerHTML, /old\.png · no longer stored, attach it again/, "a swept file is shown, not dropped");
  assert.deepEqual(missing.map((attachment) => attachment.name), ["old.png"], "the caller hears which file is gone");
  assert.equal(tab.$("#steer-message").hidden, true, "a busy turn offers no steer for attached files");
  assert.equal(tab.$("#composer-note").textContent, "Attached files can't join a running turn. Send queues them for next.");
}

{
  const tab = await loaded();
  await takenBack(tab);
  tab.$("#send").dataset.mode = "send";
  await tab.context.send();
  await settled();
  const sent = tab.requests.at(-1);
  assert.equal(sent.action, "send");
  assert.deepEqual(sent.attachments, [shot.id], "the send carries every stored attachment and no swept one");
  assert.doesNotMatch(tab.$("#composer-attachments").innerHTML, /shot\.png/, "a sent attachment leaves the composer");
  assert.match(tab.$("#composer-attachments").innerHTML, /old\.png/, "a swept one stays until the person removes it");
}

{
  const tab = await loaded();
  await takenBack(tab);
  tab.$("#message").value = "now";
  await tab.context.send("steer");
  await settled();
  assert.equal(tab.requests.at(-1).action, "queue-cancel", "a steer with attached files is never sent");
  assert.ok(tab.toasts.includes("Attached files can't join a running turn. Send queues them for next."));
  assert.match(tab.$("#composer-attachments").innerHTML, /shot\.png/, "and the attachments stay");
}

{
  const tab = await loaded();
  await takenBack(tab);
  tab.$("#send").dataset.mode = "send";
  tab.answers.push(lost);
  await tab.context.send();
  await settled();
  const pendingSends = () => [...tab.values].filter(([key]) => key.startsWith("agenthail.pendingSend.")).map(([, raw]) => JSON.parse(raw));
  const [stored] = pendingSends();
  assert.deepEqual(stored.attachments, [shot], "an unconfirmed send keeps its attachments in the stored send");
  assert.equal(tab.$("#message").value, "");
  tab.$("#send-status-put-back").listeners.click();
  assert.equal(tab.$("#message").value, "look at this", "Put back restores the text");
  assert.match(tab.$("#composer-attachments").innerHTML, /shot\.png/, "and its attachments");
  assert.equal(pendingSends().length, 0);
}

{
  const values = new Map();
  await takenBack(await loaded({ values }));
  const reloaded = await loaded({ values });
  reloaded.context.syncComposerAction();
  assert.match(reloaded.$("#composer-attachments").innerHTML, /shot\.png/, "taken-back files survive a reload, since the dashboard cannot attach them again");
  reloaded.context.applyRekeys({ a: "a2" });
  reloaded.context.app.selected = { id: "a2", alias: "a", status: "busy" };
  reloaded.context.syncComposerAction();
  assert.match(reloaded.$("#composer-attachments").innerHTML, /shot\.png/, "and follow the conversation when its id changes");
}

{
  const tab = await loaded();
  await takenBack(tab);
  tab.$("#send").dataset.mode = "send";
  tab.answers.push(refused("This session has ended."));
  await tab.context.send();
  await settled();
  assert.equal(tab.$("#message").value, "look at this", "a refused send comes back");
  assert.match(tab.$("#composer-attachments").innerHTML, /shot\.png/, "with its attachments");
  assert.ok(tab.toasts.includes("This session has ended."));
}

{
  const tab = await loaded({ storageFull: true });
  await takenBack(tab);
  assert.match(tab.$("#composer-attachments").innerHTML, /shot\.png/, "full storage still keeps the files in this tab");
  tab.$("#send").dataset.mode = "send";
  await tab.context.send();
  await settled();
  assert.deepEqual(tab.requests.at(-1).attachments, [shot.id], "and sends them");
}

{
  const tab = await loaded({ status: "idle" });
  await takenBack(tab);
  tab.$("#composer-attachments").listeners.click({ target: { closest: () => ({ dataset: { removeAttachment: shot.id } }) } });
  tab.$("#composer-attachments").listeners.click({ target: { closest: () => ({ dataset: { removeAttachment: gone.id } }) } });
  assert.equal(tab.$("#composer-attachments").hidden, true, "removing every chip hides the row");
  tab.$("#message").value = "";
  tab.context.syncComposerAction();
  assert.equal(tab.$("#send").disabled, true, "nothing left to send");
}

console.log("queued attachments: ok");
