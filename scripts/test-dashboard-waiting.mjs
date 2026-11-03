import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";

const panel = { innerHTML: "", hidden: true };
const context = {
  $: (selector) => (selector === "#request-panel" ? panel : null),
  escape: (value) => String(value).replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`),
  displayName: (session) => session.name,
  backgroundWaiting: () => false,
  statusLabel: (status) => status,
};
await dashboardModules(["presence", "requests", "state"], context);

const restartable = { lifecycle: { stop: true, resume: true } };
const session = (runtimeKind, waitingOn, capabilities = {}) => ({ name: "builder", runtimeKind, waitingOn, capabilities });
const approval = { kind: "approval", id: "q" };

assert.equal(context.presenceLabel(session("claude-host", { ...approval, answerable: true }, restartable)), "Waiting on you");
assert.equal(context.presenceLabel(session("claude-cli", approval)), "Waiting in its terminal");
assert.equal(context.presenceLabel(session("codex-desktop", approval)), "Waiting on you");
assert.equal(context.presenceLabel(session("claude-host", approval, restartable)), "Prompt out of reach");
assert.equal(context.presenceLabel(session("claude-bg", approval, restartable)), "Prompt out of reach");
assert.equal(context.presenceLabel(session("codex-app-server", approval)), "Waiting on you");
assert.equal(context.presenceLabel(session("claude-cli", { kind: "dialog", id: "d" })), "Dialog open in its terminal");

const resetsAt = "2026-10-22T18:20:00Z";
const limitedLabel = context.presenceLabel({ name: "builder", status: "idle", usageLimit: { resetsAt } });
assert.match(limitedLabel, /^Usage limit, resets /);
assert.ok(limitedLabel.endsWith(new Date(resetsAt).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })), limitedLabel);
assert.equal(context.presenceLabel({ name: "builder", status: "idle", usageLimit: {} }), "Usage limit reached");
const resuming = context.presenceLabel({ name: "builder", status: "idle", usageLimit: { resetsAt }, limitResume: { state: "waiting", resetsAt } });
assert.match(resuming, /^Usage limit, resumes /);
assert.equal(context.presenceLabel({ name: "builder", status: "idle", limitResume: { state: "waiting" } }), "Usage limit, resumes when it lifts");
assert.equal(context.presenceLabel({ name: "builder", status: "idle", limitResume: { state: "canceled", resetsAt } }), "Stopped by usage limit");
assert.equal(context.presenceLabel({ name: "builder", status: "idle", limitResume: { state: "lapsed" } }), "Stopped by usage limit");

const hosted = context.renderElsewhereWait(session("claude-host", approval, restartable));
assert.match(hosted, /builder is waiting on a prompt Agenthail can&#39;t reach|builder is waiting on a prompt Agenthail can't reach/);
assert.match(hosted, /data-action="session-restart"[^>]*>Stop and resume</);
const terminal = context.renderElsewhereWait(session("claude-cli", approval));
assert.match(terminal, /builder needs your approval/);
assert.match(terminal, /in the session&#39;s terminal|in the session's terminal/);
assert.doesNotMatch(terminal, /session-restart/);
const codex = context.renderElsewhereWait(session("codex-app-server", approval));
assert.match(codex, /builder needs your approval/);
assert.match(codex, /where the session runs/);
assert.doesNotMatch(codex, /session-restart/);

const fetched = [];
const held = { id: "c:question:held:x-1", kind: "question", tool: "AskUserQuestion", alwaysAllow: false, questions: [{ question: "Which color?", header: "Color", multiSelect: false, options: [{ label: "Red", description: "Warm" }] }] };
context.fetch = async (url) => {
  fetched.push(url);
  return { ok: true, json: async () => ({ permissionMode: "", requests: [held] }) };
};
context.app.history = { capabilities: { respond: false }, readOnly: false };
context.app.selected = { name: "builder", id: "c", runtimeKind: "claude-cli", capabilities: { respond: true }, waitingOn: { kind: "question", id: held.id, answerable: true } };
await context.syncRequests();
assert.equal(fetched.length, 1, "a question held after the session opened is read with the live session's capabilities");
assert.match(panel.innerHTML, /Which color\?/, "the held question shows its card");
assert.doesNotMatch(panel.innerHTML, /can&#39;t answer|can't answer/);
console.log("dashboard waiting places ok");
