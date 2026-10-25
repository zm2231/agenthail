import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";

const context = { escape: (value) => String(value).replaceAll("<", "&lt;") };
await dashboardModules(["audit"], context);
context.label = context.auditKindLabel;
context.heading = context.auditHeading;
context.route = context.auditRoute;

assert.equal(context.label("peer_send"), "Peer send");
assert.equal(context.label("transport-accepted"), "Transport accepted");
assert.equal(context.label(""), "Event");
assert.equal(context.route({ source: "claude/builder", target: "claude/builder" }), "claude/builder");
assert.equal(context.route({ source: "codex/lead", target: "claude/builder" }), 'codex/lead <span class="route-arrow">→</span> claude/builder');
assert.equal(context.route({}), "Agenthail");
assert.equal(context.heading({ kind: "delivered", operation: "compact" }), "Delivered · Compact");
assert.equal(context.heading({ kind: "delivered", operation: "message" }), "Delivered");
assert.equal(context.heading({ kind: "peer_send" }), "Peer send");
console.log("dashboard audit tests passed");
