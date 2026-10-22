import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";

const source = fs.readFileSync(new URL("../internal/daemon/dashboard/app.js", import.meta.url), "utf8");
const start = source.indexOf("function auditKindLabel");
const end = source.indexOf("function signinOffHint", start);
assert(start >= 0 && end > start);
const context = { escape: (value) => String(value).replaceAll("<", "&lt;") };
vm.runInNewContext(source.slice(start, end) + "\nthis.label = auditKindLabel; this.route = auditRoute;", context);

assert.equal(context.label("peer_send"), "Peer send");
assert.equal(context.label("transport-accepted"), "Transport accepted");
assert.equal(context.label(""), "Event");
assert.equal(context.route({ source: "claude/builder", target: "claude/builder" }), "claude/builder");
assert.equal(context.route({ source: "codex/lead", target: "claude/builder" }), 'codex/lead <span class="route-arrow">→</span> claude/builder');
assert.equal(context.route({}), "Agenthail");
console.log("dashboard audit tests passed");
