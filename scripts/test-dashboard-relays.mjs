import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";

const source = fs.readFileSync(new URL("../internal/daemon/dashboard/app.js", import.meta.url), "utf8");
const start = source.indexOf("function renderOperations()");
const end = source.indexOf("function auditKindLabel", start);
assert(start >= 0 && end > start);
const elements = new Map();
const context = {
  app: { state: { queue: [], channels: [], history: [], relays: [
    { id: 1, from: "source", to: "target", pattern: "READY", handoff: "goal_completed", active: true, pendingCount: 3, lastError: "lookup <failed>", fireCount: 2 },
    { id: 2, from: "source", to: "other", pattern: ".*", handoff: "turn_finished", active: true, pendingCount: 0 },
  ] } },
  $: (key) => { if (!elements.has(key)) elements.set(key, {}); return elements.get(key); },
  document: { querySelectorAll: () => [] },
  renderAudit: () => {}, renderSettings: () => {},
  escape: (value) => String(value ?? "").replaceAll("<", "&lt;").replaceAll(">", "&gt;"),
};
vm.runInNewContext(source.slice(start, end) + "\nrenderOperations();", context);
const html = elements.get("#relay-list").innerHTML;
assert.match(html, /Goal completed/);
assert.match(html, /Turn finished/);
assert.match(html, /pending 3/);
assert.match(html, /queued 2/);
assert.match(html, /lookup &lt;failed&gt;/);
assert(!html.includes("lookup <failed>"));
const form = fs.readFileSync(new URL("../internal/daemon/dashboard/index.html", import.meta.url), "utf8");
assert.match(form, /select name="handoff"/);
assert.match(form, /value="goal_completed"/);
console.log("dashboard relay rendering tests passed");
