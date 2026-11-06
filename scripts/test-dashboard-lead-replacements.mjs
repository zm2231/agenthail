import "./test-home.mjs";
import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";
import vm from "node:vm";

const elements = { panel: {}, list: {} };
const app = { state: { sessions: [], leadReplacements: [{ id: 7, projectId: "p", name: "release-lead", sessionId: "new", previousSessionId: "old", changedBy: "the daemon", reason: "conversation old no longer exists <gone>", at: "2026-11-03T12:00:00Z" }] }, selected: null };
const context = vm.createContext({ app, $: (selector) => (selector.endsWith("panel") ? elements.panel : elements.list), escape: (value) => String(value).replaceAll("<", "&lt;").replaceAll(">", "&gt;"), timeAgo: () => "just now" });

await dashboardModules(["overview"], context);
vm.runInContext("renderLeadReplacements()", context);
assert.equal(elements.panel.hidden, false);
assert.match(elements.list.innerHTML, /@release-lead/);
assert.match(elements.list.innerHTML, /data-session="new"[^>]*>Open</, "the new conversation opens from the row");
assert.match(elements.list.innerHTML, /data-lead-replacement-ack="7"[^>]*>Acknowledge</);
assert.match(elements.list.innerHTML, /Replaced by the daemon/);
assert.ok(elements.list.innerHTML.includes("&lt;gone&gt;"), "the reason is escaped");
app.state.leadReplacements = [];
vm.runInContext("renderLeadReplacements()", context);
assert.equal(elements.panel.hidden, true);

let sent;
context.crypto = { randomUUID: () => "ack-1" };
context.fetch = async (url, options) => { sent = { url, body: JSON.parse(options.body) }; return { ok: true, json: async () => ({ ok: true }) }; };
await dashboardModules(["actions", "delivery-labels"], context);
await vm.runInContext('action("lead-replacement-ack", {replacementId: 7})', context);
assert.equal(sent.url, "/api/action");
assert.equal(sent.body.action, "lead-replacement-ack");
assert.equal(sent.body.replacementId, 7, "acknowledging needs no selected conversation");
console.log("dashboard lead replacements ok");
