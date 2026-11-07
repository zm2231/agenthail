import "./test-home.mjs";
import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";

const listeners = {};
const calls = [];
const posted = [];
let refuse = false;
const replacement = (id) => ({ id, projectId: "p", name: `lead-${id}`, sessionId: `new-${id}`, previousSessionId: `old-${id}`, changedBy: "the daemon", reason: "gone", at: "2026-11-03T12:00:00Z" });
const app = { state: { sessions: [], leadReplacements: [replacement(7), replacement(8)] }, selected: null, requests: { requests: [] }, requestsKey: "x", filters: {}, pendingIdempotency: new Map() };
const context = {
  app,
  crypto: { randomUUID: () => "id" },
  document: { addEventListener: (type, listener) => (listeners[type] ||= []).push(listener) },
  fetch: async (url, options) => {
    posted.push({ url, body: JSON.parse(options.body) });
    if (refuse) return { ok: false, status: 409, text: async () => JSON.stringify({ error: { message: "refused", code: "conflict" } }) };
    return { ok: true, json: async () => ({ ok: true }) };
  },
  respondToRequest: async () => calls.push({ name: "respond" }),
  load: async () => calls.push({ name: "load" }),
  selectSession: async (id) => calls.push({ name: "select", id }),
  renderLeadReplacements: () => calls.push({ name: "render" }),
  toast: (message) => calls.push({ name: "toast", message }),
  friendlyError: (error) => error.message,
  holdAgentRow: () => {},
  withHolder: (_, payload) => payload,
};
await dashboardModules(["conversation-events", "requests"], context);
context.bindConversationEvents();
context.bindRequestEvents();

function make(attribute, dataset) {
  const element = { dataset, disabled: false };
  element.closest = (selector) => (selector === `[${attribute}]` ? element : null);
  return element;
}
async function click(target) {
  for (const listener of listeners.click) await listener({ target, preventDefault() {}, stopPropagation() {} });
}
const plain = (value) => JSON.parse(JSON.stringify(value));

const ack = make("data-lead-replacement-ack", { leadReplacementAck: "7" });
await click(ack);
assert.equal(posted.length, 1);
assert.deepEqual(plain(posted[0].body), { action: "lead-replacement-ack", replacementId: 7 }, "acknowledging posts only the replacement, with no conversation selected");
assert.deepEqual(app.state.leadReplacements.map((item) => item.id), [8], "only the acknowledged row goes");
assert.ok(calls.some((call) => call.name === "render"));
assert.equal(ack.disabled, true, "the button stays disabled once acknowledged");

calls.length = 0;
posted.length = 0;
refuse = true;
const failing = make("data-lead-replacement-ack", { leadReplacementAck: "8" });
await click(failing);
assert.equal(posted.length, 1);
assert.deepEqual(app.state.leadReplacements.map((item) => item.id), [8], "a refused acknowledgement keeps the row");
assert.equal(failing.disabled, false, "and re-enables the button");
assert.ok(calls.some((call) => call.name === "toast" && call.message === "refused"));
refuse = false;

calls.length = 0;
posted.length = 0;
app.selected = { id: "old-lead" };
const withdraw = make("data-request-withdraw", { requestWithdraw: "c:question:old:x-2" });
await click(withdraw);
assert.equal(posted.length, 1);
assert.deepEqual(plain(posted[0].body), { action: "request-withdraw", sessionId: "old-lead", requestId: "c:question:old:x-2" }, "withdrawing posts the request for the selected conversation");
assert.ok(calls.some((call) => call.name === "load"), "and reloads");
assert.ok(calls.some((call) => call.name === "select" && call.id === "old-lead"), "and reopens the conversation");
assert.ok(!calls.some((call) => call.name === "respond"), "withdrawing never answers the request");
assert.equal(withdraw.disabled, true);
console.log("dashboard lead replacement clicks ok");
