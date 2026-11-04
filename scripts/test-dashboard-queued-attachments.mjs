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

const shot = { id: "upload:aa/shot.png", name: "shot.png", mediaType: "image/png", bytes: 138 };
const old = { id: "upload:bb/old.png", name: "old.png", mediaType: "image/png", bytes: 90 };
const gone = { ...old, missing: true };
const queuedImage = { id: 7, sessionId: "a", status: "pending", operation: "message", message: "look at this", attachments: [shot, old] };

async function loaded({ status = "busy", values = new Map(), refuses = () => false, selected = "a", queue = [queuedImage], capabilities = { steerImages: true } } = {}) {
  const elements = new Map();
  const $ = (selector) => {
    if (!elements.has(selector)) elements.set(selector, element());
    return elements.get(selector);
  };
  const requests = [];
  const answers = [];
  const toasts = [];
  const storage = { full: false };
  let ids = 0;
  const sessions = [{ id: "a", alias: "a", status }, { id: "b", alias: "b", status }];
  const context = {
    $,
    app: {
      selected: sessions.find((session) => session.id === selected) || null,
      state: { sessions, queue },
      history: { capabilities: { steer: true, interrupt: true, ...capabilities } },
      drafts: null,
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
        if (storage.full || refuses(key)) throw new Error("quota");
        values.set(key, value);
      },
      removeItem: (key) => values.delete(key),
    },
    navigator: {},
    window: {
      listeners: {},
      addEventListener(type, listener) { (this.listeners[type] ||= []).push(listener); },
      confirm: () => true,
    },
    crypto: { randomUUID: () => `id-${++ids}` },
    fetch: async (_url, request) => {
      requests.push(JSON.parse(request.body));
      const answer = answers.shift();
      if (answer) return answer();
      return { ok: true, json: async () => ({ ok: true }) };
    },
    toast: (text) => toasts.push(text),
    load: async () => {},
    selectSession: async (id) => {
      if (context.app.selected) context.app.drafts.set(context.app.selected.id, $("#message").value);
      context.app.selected = context.app.state.sessions.find((session) => session.id === id);
      $("#message").value = context.app.drafts.get(id) || "";
    },
    renderHolderPicker() {},
    chosenHolder: () => undefined,
    sharedHolders: () => [],
    withHolder: (_id, payload) => payload,
    deliveryStatusLabel: () => "Sent",
    escape: (text) => String(text),
    rememberToolRow() {},
  };
  await dashboardModules(["draft-store", "composer", "send-status", "pending-send", "composer-attachments", "queue-take-back", "rekeys"], context);
  context.app.drafts = context.persistentDrafts();
  if (context.app.selected) $("#message").value = context.app.drafts.get(context.app.selected.id) || "";
  context.bindComposerEvents();
  return { context, $, requests, answers, toasts, values, storage };
}

const settled = () => new Promise((resolve) => setTimeout(resolve, 5));
const answered = (body) => () => ({ ok: true, json: async () => body });
const refused = (message) => () => ({ ok: false, status: 409, text: async () => JSON.stringify({ error: { message } }) });
const lost = () => { throw new TypeError("network lost"); };
const canceled = answered({ ok: true, result: { message: "look at this", attachments: [shot, gone] } });
const chips = (tab) => tab.$("#composer-attachments").innerHTML;
const pendingSends = (tab) => [...tab.values].filter(([key]) => key.startsWith("agenthail.pendingSend.")).map(([, raw]) => JSON.parse(raw));
const drafts = (tab) => [...tab.values].filter(([key]) => key.startsWith("agenthail.draft.")).map(([, raw]) => JSON.parse(raw));

async function takenBack(tab, queueId = 7) {
  tab.answers.push(canceled);
  const missing = await tab.context.takeBackQueued(queueId);
  assert.equal(tab.requests.at(-1).action, "queue-cancel");
  assert.equal(tab.requests.at(-1).queueId, queueId);
  return missing;
}

{
  const tab = await loaded();
  const missing = await takenBack(tab);
  assert.equal(tab.$("#message").value, "look at this", "take back puts the text in the composer");
  assert.equal(tab.$("#composer-attachments").hidden, false, "and shows its attachments");
  assert.match(chips(tab), /shot\.png/);
  assert.match(chips(tab), /old\.png · no longer stored, attach it again/, "a file the daemon reports swept is shown, not dropped");
  assert.deepEqual(missing.map((attachment) => attachment.name), ["old.png"], "the caller hears which file is gone");
  assert.equal(tab.$("#steer-message").hidden, true, "a row whose file is gone can't steer");
  assert.equal(tab.$("#composer-note").textContent, "An attached file is gone. Remove it to steer.");
}

{
  const stored = { ...queuedImage, id: 9, attachments: [shot] };
  const tab = await loaded({ queue: [stored] });
  tab.answers.push(answered({ ok: true, result: { message: "look at this", attachments: [shot] } }));
  await tab.context.takeBackQueued(9);
  tab.context.syncComposerAction();
  assert.equal(tab.$("#steer-message").hidden, false, "an image can steer where the session's steer takes images");
  tab.answers.push(answered({ ok: true, result: { evidence: "delivered" } }));
  await tab.context.send("steer");
  await settled();
  const sent = tab.requests.at(-1);
  assert.equal(sent.action, "send", "the steer action takes text only");
  assert.equal(sent.busyDelivery, "steer");
  assert.deepEqual(sent.attachments, [shot.id]);
  assert.equal(chips(tab).includes("shot.png"), false, "and the chip leaves the composer");
}

for (const [capabilities, note] of [
  [{ steerImages: false }, "Attached images can't join a running turn on this session. Send queues them for next."],
  [{}, "Update Agenthail on this computer to steer attachments. Send queues them for next."],
]) {
  const stored = { ...queuedImage, id: 9, attachments: [shot] };
  const tab = await loaded({ queue: [stored], capabilities });
  tab.answers.push(answered({ ok: true, result: { message: "look at this", attachments: [shot] } }));
  await tab.context.takeBackQueued(9);
  tab.context.syncComposerAction();
  assert.equal(tab.$("#steer-message").hidden, true, "no steer where the attachment would only wait");
  assert.equal(tab.$("#composer-note").textContent, note);
}

{
  const notes = { id: "upload:cc/notes.txt", name: "notes.txt", mediaType: "text/plain", bytes: 9 };
  const tab = await loaded({ queue: [{ ...queuedImage, id: 9, attachments: [notes] }], capabilities: { steerImages: false } });
  tab.answers.push(answered({ ok: true, result: { message: "look at this", attachments: [notes] } }));
  await tab.context.takeBackQueued(9);
  tab.context.syncComposerAction();
  assert.equal(tab.$("#steer-message").hidden, false, "a file steers as its path even where images can't");
}

{
  const tab = await loaded({ selected: "b" });
  tab.$("#message").value = "for b";
  tab.$("#message").listeners.input();
  await takenBack(tab);
  assert.equal(tab.context.app.selected.id, "a", "take back for a listed conversation opens it");
  assert.equal(tab.$("#message").value, "look at this", "and lands there");
  assert.equal(tab.context.app.drafts.get("b"), "for b", "the other conversation's draft is left alone");
}

{
  const tab = await loaded({ queue: [{ ...queuedImage, sessionId: "gone" }] });
  assert.equal(tab.context.canTakeBack(tab.context.app.state.queue[0]), false, "an unlisted conversation offers Cancel only");
  await assert.rejects(tab.context.takeBackQueued(7), /Open this conversation/);
  assert.equal(tab.requests.length, 0, "and nothing is canceled");
}

{
  const values = new Map();
  const tab = await loaded({ values });
  await takenBack(tab);
  const keys = [...values.keys()];
  assert.ok(keys.some((key) => key.startsWith("agenthail.draft.")), "the taken-back text is saved as the conversation's draft");
  const reloaded = await loaded({ values });
  reloaded.context.syncComposerAction();
  assert.equal(reloaded.$("#message").value, "look at this", "a reload brings the text back");
  assert.match(chips(reloaded), /shot\.png/, "and the files");
  reloaded.$("#message").value = "look at this, edited";
  reloaded.$("#message").listeners.input();
  const again = await loaded({ values });
  assert.equal(again.$("#message").value, "look at this, edited", "an edit survives the next reload");
  again.context.applyRekeys({ a: "a2" });
  assert.equal(again.context.app.drafts.get("a2"), "look at this, edited", "the draft follows the conversation when its id changes");
  again.context.app.selected = { id: "a2", alias: "a", status: "busy" };
  again.context.syncComposerAction();
  assert.match(chips(again), /shot\.png/, "and so do its files");
}

{
  const values = new Map();
  const tab = await loaded({ values });
  await takenBack(tab);
  tab.$("#send").dataset.mode = "send";
  tab.answers.push(lost);
  await tab.context.send();
  await settled();
  assert.equal(drafts(tab).length, 0, "the draft goes once the pending send holds the message");
  assert.equal(pendingSends(tab).length, 1, "so one stored copy always exists");
  assert.deepEqual(pendingSends(tab)[0].attachments, [shot], "an unconfirmed send keeps its attachments in the stored send");
  tab.$("#send-status-put-back").listeners.click();
  assert.equal(tab.$("#message").value, "look at this", "Put back restores the text");
  assert.match(chips(tab), /shot\.png/, "and its attachments");
  assert.equal(pendingSends(tab).length, 0);
  assert.equal(drafts(tab)[0]?.text, "look at this", "and saves them as the draft again");
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
  assert.doesNotMatch(chips(tab), /shot\.png/, "a sent attachment leaves the composer");
  assert.match(chips(tab), /old\.png/, "a swept one stays until the person removes it");
}

{
  const tab = await loaded();
  await takenBack(tab);
  tab.$("#send").dataset.mode = "send";
  tab.answers.push(refused("This session has ended."));
  await tab.context.send();
  await settled();
  assert.equal(tab.$("#message").value, "look at this", "a refused send comes back");
  assert.match(chips(tab), /shot\.png/, "with its attachments");
  assert.ok(tab.toasts.includes("This session has ended."));
}

{
  const tab = await loaded();
  await takenBack(tab);
  tab.$("#message").value = "now";
  await tab.context.send("steer");
  await settled();
  assert.equal(tab.requests.at(-1).action, "queue-cancel", "a steer with attached files is never sent");
  assert.ok(tab.toasts.includes("An attached file is gone. Remove it to steer."));
  assert.match(chips(tab), /shot\.png/, "and the attachments stay");
}

{
  const tab = await loaded({ queue: [{ ...queuedImage, id: 8, message: "", attachments: [old] }] });
  tab.answers.push(answered({ ok: true, result: { message: "", attachments: [gone] } }));
  await tab.context.takeBackQueued(8);
  tab.$("#message").value = "";
  tab.context.syncComposerAction();
  assert.equal(tab.$("#steer-message").hidden, true, "a missing file alone still hides Steer");
  assert.notEqual(tab.$("#send").dataset.mode, "interrupt", "and does not turn Send into Stop");
  const before = tab.requests.length;
  for (const [typed, mode] of [["now", "steer"], ["/steer now", "send"], ["/compact", "send"]]) {
    tab.$("#message").value = typed;
    tab.$("#send").dataset.mode = "send";
    await tab.context.send(mode);
  }
  assert.equal(tab.requests.length, before, "no steer or command goes out while a file is held");
  assert.match(chips(tab), /old\.png/, "and the chip stays");
}

{
  const tab = await loaded();
  tab.storage.full = true;
  tab.$("#message").value = "typed";
  await assert.rejects(tab.context.takeBackQueued(7), /stays queued/, "full storage refuses a take back it could not keep");
  assert.equal(tab.requests.length, 0, "and leaves the message queued");
  assert.equal(tab.$("#message").value, "typed", "the composer is as it was");
  assert.equal(tab.$("#composer-attachments").hidden, true);
}

{
  const tab = await loaded();
  tab.$("#message").value = "typed";
  tab.answers.push(refused("queue item 7 is not pending or dead-lettered"));
  await assert.rejects(tab.context.takeBackQueued(7), /not pending/);
  assert.equal(tab.$("#message").value, "typed", "a refused cancel undoes the take back");
  assert.equal(tab.$("#composer-attachments").hidden, true, "including its files");
}

{
  const tab = await loaded();
  tab.answers.push(lost);
  await assert.rejects(tab.context.takeBackQueued(7), /wasn't confirmed/);
  assert.equal(tab.$("#message").value, "look at this", "an unconfirmed cancel keeps the copy in the composer");
  assert.match(chips(tab), /shot\.png/);
}

{
  const tab = await loaded();
  await takenBack(tab);
  tab.$("#send").dataset.mode = "send";
  tab.answers.push(() => {
    tab.storage.full = true;
    return refused("Not now.")();
  });
  await tab.context.send();
  await settled();
  assert.match(chips(tab), /shot\.png/, "files that storage refuses on a refusal stay in this tab");
  assert.ok(tab.toasts.some((text) => /stay in this tab only; send them before you reload/.test(text)), "and the person is told they won't survive a reload");
}

{
  const tab = await loaded();
  await takenBack(tab);
  tab.$("#send").dataset.mode = "send";
  tab.answers.push(lost);
  await tab.context.send();
  await settled();
  tab.context.app.state.sessions = [{ id: "b", alias: "b" }];
  tab.context.app.selected = { id: "b", alias: "b", status: "idle" };
  tab.$("#message").value = "";
  tab.$("#send-status-put-back").listeners.click();
  assert.equal(tab.$("#message").value, "", "Put back for an unlisted conversation leaves the selected one alone");
  tab.context.syncComposerAction();
  assert.equal(tab.$("#composer-attachments").hidden, true, "and puts no files into it");
  assert.equal(pendingSends(tab).length, 1, "the send stays stored for its own conversation");
  assert.equal(pendingSends(tab)[0].sessionId, "a");
  assert.deepEqual(pendingSends(tab)[0].attachments, [shot]);
}

{
  const values = new Map();
  const queue = [queuedImage, { ...queuedImage, id: 8, message: "", attachments: [old] }];
  const first = await loaded({ values, queue });
  const second = await loaded({ values, queue });
  first.answers.push(answered({ ok: true, result: { message: "look at this", attachments: [shot] } }));
  second.answers.push(answered({ ok: true, result: { message: "", attachments: [old] } }));
  await first.context.takeBackQueued(7);
  await second.context.takeBackQueued(8);
  for (const tab of [first, second]) {
    tab.context.syncComposerAction();
    assert.match(chips(tab), /shot\.png[\s\S]*old\.png/, "two tabs taking back files for one conversation keep both");
  }
}

{
  const values = new Map();
  await takenBack(await loaded({ values }));
  const moving = await loaded({ values, refuses: (key) => key.includes('"a2"') });
  moving.context.applyRekeys({ a: "a2" });
  assert.ok(moving.toasts.some((text) => /renamed conversation/.test(text)), "a file move storage refuses is reported");
  const reloaded = await loaded({ values });
  reloaded.context.syncComposerAction();
  assert.match(chips(reloaded), /shot\.png/, "and the stored file is not dropped");
}

{
  const tab = await loaded({ status: "idle" });
  await takenBack(tab);
  tab.$("#composer-attachments").listeners.click({ target: { closest: () => ({ dataset: { removeAttachment: shot.id } }) } });
  tab.$("#composer-attachments").listeners.click({ target: { closest: () => ({ dataset: { removeAttachment: old.id } }) } });
  assert.equal(tab.$("#composer-attachments").hidden, true, "removing every chip hides the row");
  tab.$("#message").value = "";
  tab.$("#message").listeners.input();
  tab.context.syncComposerAction();
  assert.equal(tab.$("#send").disabled, true, "nothing left to send");
  assert.equal(drafts(tab).length, 0, "an empty composer keeps no draft");
}

{
  const values = new Map();
  const tab = await loaded({ values });
  tab.$("#message").value = "unsent words";
  tab.$("#message").listeners.input();
  const moving = await loaded({ values, refuses: (key) => key.includes('"a2"') });
  moving.context.applyRekeys({ a: "a2" });
  const reloaded = await loaded({ values });
  assert.equal(reloaded.context.app.drafts.get("a"), "unsent words", "a draft move storage refuses keeps the draft where it was");
}

{
  const values = new Map();
  const stale = await loaded({ values });
  stale.$("#message").value = "old words";
  stale.$("#message").listeners.input();
  const current = await loaded({ values });
  assert.equal(current.$("#message").value, "old words");
  current.$("#message").value = "newer words";
  current.$("#message").listeners.input();
  stale.context.app.drafts.set("a", stale.$("#message").value);
  assert.equal((await loaded({ values })).context.app.drafts.get("a"), "newer words", "a stale tab switching away does not overwrite a newer draft");
  current.$("#message").value = "";
  current.$("#message").listeners.input();
  stale.context.app.drafts.set("a", stale.$("#message").value);
  assert.equal((await loaded({ values })).context.app.drafts.get("a"), undefined, "nor bring back text another tab cleared");
}

{
  const values = new Map();
  const watching = await loaded({ values });
  watching.$("#message").value = "first";
  watching.$("#message").listeners.input();
  const other = await loaded({ values });
  other.$("#message").value = "first, then more";
  other.$("#message").listeners.input();
  const key = [...values.keys()].find((name) => name.startsWith("agenthail.draft."));
  for (const listener of watching.context.window.listeners.storage) listener({ key, newValue: values.get(key) });
  assert.equal(watching.$("#message").value, "first, then more", "an untouched composer shows the draft another tab saved");
}

{
  const values = new Map();
  const watching = await loaded({ values });
  const other = await loaded({ values });
  other.$("#message").value = "older";
  other.$("#message").listeners.input();
  const key = [...values.keys()].find((name) => name.startsWith("agenthail.draft."));
  const late = { key, newValue: values.get(key) };
  watching.$("#message").value = "newer";
  watching.$("#message").listeners.input();
  for (const listener of watching.context.window.listeners.storage) listener(late);
  assert.equal(watching.$("#message").value, "newer", "a late event from another tab does not replace newer typing");
  assert.equal((await loaded({ values })).context.app.drafts.get("a"), "newer", "and the saved draft stays newer");
  for (const listener of watching.context.window.listeners.storage) listener({ key, newValue: null });
  assert.equal(watching.$("#message").value, "newer", "nor does a late clear");
}

{
  const values = new Map();
  const local = await loaded({ values });
  const other = await loaded({ values });
  local.storage.full = true;
  local.$("#message").value = "unsaved local edit";
  local.$("#message").listeners.input();
  local.storage.full = false;
  other.$("#message").value = "other tab edit";
  other.$("#message").listeners.input();
  const key = [...values.keys()].find((name) => name.startsWith("agenthail.draft."));
  for (const listener of local.context.window.listeners.storage) listener({ key, newValue: values.get(key) });
  assert.equal(local.$("#message").value, "unsaved local edit", "another tab's draft never replaces an edit this tab could not save");
  local.$("#message").listeners.input();
  assert.equal((await loaded({ values })).context.app.drafts.get("a"), "unsaved local edit", "and the edit saves once storage has room");
}

console.log("queued attachments: ok");
