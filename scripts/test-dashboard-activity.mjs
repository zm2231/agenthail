import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";

const context = {};
await dashboardModules(["session-display", "session-labels"], context);
const display = { kind: "working", label: "Daemon label", detail: "Daemon detail", quietSince: "2026-10-23T10:02:00Z", toolStartedAt: "2026-10-23T10:03:00Z" };
const row = { display, status: "idle", waitingOn: { answerable: true }, usageLimit: {}, backgroundTasks: { running: true } };
assert.equal(context.displayText(row), `Daemon label · Daemon detail · ${new Date(display.quietSince).toLocaleString()} · ${new Date(display.toolStartedAt).toLocaleString()}`);
assert.equal(context.conversationMeta(row), context.displayText(row));
for (const key of ["resetsAt", "resumesAt", "quietSince", "toolStartedAt"]) {
 const instant = "2026-10-25T12:34:56Z";
 assert.equal(context.displayText({ display: { kind: "limited", label: "Sent", detail: "Text", [key]: instant } }), `Sent · Text · ${new Date(instant).toLocaleString()}`);
}
assert.throws(() => context.displayText({ status: "busy" }), /did not send/);
assert.throws(() => context.displayText({ display: { ...display, resetsAt: "bad" } }), /Invalid display instant/);
console.log("dashboard display text and locale instants passed");
