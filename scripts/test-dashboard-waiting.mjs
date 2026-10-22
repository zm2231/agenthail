import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import vm from "node:vm";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const source = fs.readFileSync(path.join(root, "internal/daemon/dashboard/app.js"), "utf8");
const slice = (from, to) => {
  const start = source.indexOf(from);
  const end = source.indexOf(to, start);
  assert(start >= 0 && end > start, `${from} boundary missing`);
  return source.slice(start, end);
};
const context = {
  escape: (value) => String(value).replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`),
  displayName: (session) => session.name,
  backgroundWaiting: () => false,
  statusLabel: (status) => status,
};
vm.runInNewContext(
  `${slice("function waitingPlace", "function presenceTone")}\n${slice("function renderElsewhereWait", "function renderRequestCard")}\nglobalThis.presenceLabel = presenceLabel;\nglobalThis.renderElsewhereWait = renderElsewhereWait;`,
  context,
);

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
console.log("dashboard waiting places ok");
