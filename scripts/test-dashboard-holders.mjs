import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import vm from "node:vm";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const source = fs.readFileSync(path.join(root, "internal/daemon/dashboard/app.js"), "utf8");
const holdersStart = source.indexOf("function sharedHolders");
const holdersEnd = source.indexOf("function projectSection", holdersStart);
const actionStart = source.indexOf("function isNetworkFailure");
const actionEnd = source.indexOf("async function voiceRequest", actionStart);
assert(holdersStart >= 0 && holdersEnd > holdersStart, "holder picker boundary missing");
assert(actionStart >= 0 && actionEnd > actionStart, "action boundary missing");

const select = { innerHTML: "", value: "", dataset: {} };
const elements = {
  "#holder-picker": { hidden: true },
  "#holder-summary": { textContent: "" },
  "#holder-pid": select,
};
const calls = [];
const context = {
  app: { selected: null, pendingIdempotency: new Map() },
  $: (selector) => elements[selector],
  escape: (value) => String(value).replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`),
  labels: { claude: "Claude Code" },
  rawDisplayName: (session) => session.id,
  displayName: (session) => session.id,
  presenceTone: () => "idle",
  presenceLabel: () => "Ready",
  hostBadge: () => "",
  crypto: { randomUUID: (() => { let n = 0; return () => `key-${++n}`; })() },
  fetch: async (_url, request) => {
    calls.push({ key: request.headers["Idempotency-Key"], body: JSON.parse(request.body) });
    return { ok: true, json: async () => ({ ok: true }) };
  },
};
vm.runInNewContext(`${source.slice(holdersStart, holdersEnd)}\n${source.slice(actionStart, actionEnd)}\nglobalThis.renderHolderPicker = renderHolderPicker;\nglobalThis.sessionButton = sessionButton;\nglobalThis.runAction = action;\nglobalThis.runLogicalAction = logicalAction;`, context);

const shared = {
  id: "shared",
  surface: "claude",
  holders: [
    { pid: 5799, tty: "ttys001", status: "idle" },
    { pid: 31497, tty: "ttys021", status: "busy" },
  ],
};

context.app.selected = { id: "single", surface: "claude", holders: [] };
context.renderHolderPicker();
assert.equal(elements["#holder-picker"].hidden, true, "a conversation in one terminal shows no picker");
await context.runAction("send", { message: "one" }, "k", "single");
assert.equal(calls.at(-1).body.pid, undefined, "a conversation in one terminal sends without a pin");

context.app.selected = shared;
context.renderHolderPicker();
assert.equal(elements["#holder-picker"].hidden, false, "a conversation in two terminals shows the picker");
assert.match(select.innerHTML, /pid 5799, ttys001, idle/);
assert.match(select.innerHTML, /pid 31497, ttys021, busy/);
assert.match(context.sessionButton(shared), />2 terminals</, "the row names how many terminals hold it");
await context.runAction("send", { message: "unpicked" }, "k", "shared");
assert.equal(calls.at(-1).body.pid, undefined, "no terminal picked sends no pin, so the daemon refuses it");

select.value = "31497";
context.renderHolderPicker();
assert.equal(select.value, "31497", "a refresh keeps the picked terminal");
await context.runAction("steer", { message: "picked" }, "k", "shared");
assert.equal(calls.at(-1).body.pid, 31497, "a write carries the picked terminal");
await context.runAction("queue-cancel", { queueId: 4 }, "k", "shared");
assert.equal(calls.at(-1).body.pid, undefined, "an action that is not a write to the session carries no pin");

await context.runLogicalAction("send", "send", { message: "logical" });
const pinned = calls.at(-1);
select.value = "5799";
await context.runLogicalAction("send", "send", { message: "logical" });
assert.equal(calls.at(-1).body.pid, 5799);
assert.notEqual(calls.at(-1).key, pinned.key, "a different terminal is a different request");

context.app.selected = { ...shared, holders: [shared.holders[1]] };
context.renderHolderPicker();
assert.equal(elements["#holder-picker"].hidden, true, "back to one terminal hides the picker");
await context.runAction("send", { message: "after" }, "k", "shared");
assert.equal(calls.at(-1).body.pid, undefined, "back to one terminal sends without a pin");

console.log("dashboard holder picker checks passed");
