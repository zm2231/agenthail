import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";

const context = {};
await dashboardModules(["requests"], context);
const session = { name: "builder", status: "busy", runtimeKind: "claude-cli", waitingOn: { kind: "approval" }, display: { kind: "waiting", label: "Sent heading", detail: "Sent <detail>", waitingPlace: "restart" } };
const restart = context.renderElsewhereWait(session);
assert.match(restart, /Sent heading · Sent &lt;detail&gt;/);
assert.match(restart, /data-action="session-restart"/);
const terminal = context.renderElsewhereWait({ ...session, capabilities: { lifecycle: { stop: true, resume: true } }, display: { ...session.display, waitingPlace: "terminal" } });
assert.doesNotMatch(terminal, /session-restart/);
assert.match(terminal, /Sent heading · Sent &lt;detail&gt;/);
console.log("dashboard sent waiting presentation passed");
