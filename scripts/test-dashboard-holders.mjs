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
vm.runInNewContext(source.slice(source.indexOf("function livePlan"), source.indexOf("function presenceLabel")), context);
vm.runInNewContext(`${source.slice(holdersStart, holdersEnd)}\n${source.slice(actionStart, actionEnd)}\nglobalThis.renderHolderPicker = renderHolderPicker;\nglobalThis.sessionButton = sessionButton;\nglobalThis.runAction = action;\nglobalThis.runLogicalAction = logicalAction;`, context);

const shared = {
  id: "shared",
  surface: "claude",
  holders: [
    { pid: 4101, tty: "pts/1", status: "idle" },
    { pid: 4102, tty: "ttys002", status: "busy", startedAt: "2026-10-21T01:02:03.456Z" },
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
assert.match(select.innerHTML, /pid 4101, pts\/1, idle/);
assert.match(select.innerHTML, /pid 4102, ttys002, busy/);
assert.match(context.sessionButton(shared), />2 terminals</, "the row names how many terminals hold it");
assert.match(context.sessionButton({ ...shared, open: true, status: "busy", plan: { done: 3, total: 5, current: "Running gates" } }), /title="3 of 5 tasks done · Running gates"><span aria-hidden="true">3\/5<\/span><span class="sr-only">3 of 5 tasks done · Running gates<\/span>/, "an open row part way through its tasks shows the count and names the step");
assert.doesNotMatch(context.sessionButton({ ...shared, open: false, status: "idle", plan: { done: 3, total: 5 } }), /tasks done/, "a closed row keeps no badge");
await context.runAction("send", { message: "unpicked" }, "k", "shared");
assert.equal(calls.at(-1).body.pid, undefined, "no terminal picked sends no pin, so the daemon refuses it");

select.value = "4102";
context.renderHolderPicker();
assert.equal(select.value, "4102", "a refresh keeps the picked terminal");
await context.runAction("steer", { message: "picked" }, "k", "shared");
assert.equal(calls.at(-1).body.pid, 4102, "a write carries the picked terminal");
assert.equal(calls.at(-1).body.pidStartedAt, Date.parse("2026-10-21T01:02:03.456Z"), "a write carries the picked terminal's start, so a reused pid is refused");
await context.runAction("queue-cancel", { queueId: 4 }, "k", "shared");
assert.equal(calls.at(-1).body.pid, undefined, "an action that is not a write to the session carries no pin");

await context.runLogicalAction("send", "send", { message: "logical" });
const pinned = calls.at(-1);
select.value = "4101";
await context.runLogicalAction("send", "send", { message: "logical" });
assert.equal(calls.at(-1).body.pid, 4101);
assert.notEqual(calls.at(-1).key, pinned.key, "a different terminal is a different request");

context.app.selected = { ...shared, holders: [shared.holders[1]] };
context.renderHolderPicker();
assert.equal(elements["#holder-picker"].hidden, true, "back to one terminal hides the picker");
await context.runAction("send", { message: "after" }, "k", "shared");
assert.equal(calls.at(-1).body.pid, undefined, "back to one terminal sends without a pin");

console.log("dashboard holder picker checks passed");
