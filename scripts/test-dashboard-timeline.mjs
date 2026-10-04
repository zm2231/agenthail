import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import vm from "node:vm";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const source = fs.readFileSync(path.join(root, "internal/daemon/dashboard/app.js"), "utf8");
const markdownStart = source.indexOf("function inlineMarkdown");
const markdownEnd = source.indexOf("const rawDisplayName", markdownStart);
const messageStart = source.indexOf("function handoffMessage");
const messageEnd = source.indexOf("function stopLiveStream", messageStart);
const rendererStart = source.indexOf("function renderImageAttachment");
const rendererEnd = source.indexOf("function renderChat", rendererStart);
const chatStart = rendererEnd;
const chatEnd = source.indexOf("function compactTokenCount", chatStart);
const goalStart = chatEnd;
const goalEnd = source.indexOf("function renderContextUsage", goalStart);
assert(markdownStart >= 0 && markdownEnd > markdownStart, "markdown renderer boundary missing");
assert(messageStart >= 0 && messageEnd > messageStart, "message renderer boundary missing");
assert(rendererStart >= 0 && rendererEnd > rendererStart, "timeline renderer boundary missing");
assert(chatEnd > chatStart, "chat renderer boundary missing");
assert(goalEnd > goalStart, "goal renderer boundary missing");

const elements = {
  "#chat-actions": { innerHTML: "" },
  "#message": { disabled: false, placeholder: "" },
  "#send": { disabled: false },
  "#composer-note": { textContent: "" },
  "#chat-subtitle": { textContent: "" },
  "#thread-count": { textContent: "" },
  "#chat-body": {
    innerHTML: "",
    scrollTop: 0,
    scrollHeight: 0,
    clientHeight: 0,
    querySelectorAll: () => [],
    getBoundingClientRect: () => ({ top: 0 }),
  },
};
const context = {
  app: { expandedTurns: new Set(), slashCommands: [], pendingEntryScroll: false, transcriptSignature: null },
  labels: { claude: "Claude Code", codex: "Codex" },
  $: (selector) => elements[selector],
  mobileConversationView: () => false,
  syncComposerAction: () => {},
  renderSlashMenu: () => {},
  conversationMeta: () => "Claude Code",
  renderContextUsage: () => {},
  startLiveStream: () => {},
  renderGoalAttention: () => "",
  renderLiveTurn: () => {},
  alignTranscriptTop: () => {},
  escape: (value) => String(value ?? "").replace(/[&<>"']/g, (char) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#039;",
  })[char]),
};
vm.runInNewContext(`${source.slice(markdownStart, markdownEnd)}${source.slice(messageStart, messageEnd)}${source.slice(rendererStart, rendererEnd)}
globalThis.renderTimeline = renderTimeline;
globalThis.timelineSignature = timelineSignature;
globalThis.renderClaudeMetadata = renderClaudeMetadata;
${source.slice(chatStart, chatEnd)}
${source.slice(goalStart, goalEnd)}
globalThis.renderChat = renderChat;
globalThis.goalStatusLabel = goalStatusLabel;`, context);

const session = { id: "session-1", surface: "claude" };
const items = [
  { id: "message-1", kind: "message", role: "user", text: "Read the report", title: "User request", timestamp: "2026-10-04T12:00:00Z" },
  { id: "call-1", kind: "toolCall", title: "Read", text: "report.md", callId: "call-7", status: "running", timestamp: "2026-10-04T12:00:01Z" },
  { id: "result-1", kind: "toolResult", title: "Read result", text: "report contents", callId: "call-7", status: "complete", timestamp: "2026-10-04T12:00:02Z" },
  { id: "reason-1", kind: "reasoning", title: "Reasoning", text: "Need to compare the evidence.", timestamp: "2026-10-04T12:00:03Z" },
  { id: "image-1", kind: "attachment", text: "A chart", attachment: { id: "attachment-1", mediaType: "image/png" }, timestamp: "2026-10-04T12:00:04Z" },
  { id: "future-1", kind: "futureEvent", title: "Future event", text: "<script>alert('x')</script>", status: "unknown", timestamp: "2026-10-04T12:00:05Z", truncated: true, truncationReason: "bounded", bodyRef: "blob://body-1" },
];
const html = context.renderTimeline(items, session);
for (const expected of ["Read the report", "report.md", "report contents", "call-7", "running", "complete", "2026-10-04T12:00:03Z", "attachment-1", "Content truncated", "blob://body-1", "Future event", "futureEvent"]) {
  assert(html.includes(expected), `timeline omitted ${expected}`);
}
assert(html.includes("<details"), "tool and reasoning items should be collapsible");
assert(!html.includes("<script>alert"), "timeline body must not permit arbitrary HTML");
assert(html.includes("&lt;script&gt;"), "unknown body should use the escaped fallback");

const claudeMetadata = context.renderClaudeMetadata(
  [{ jobId: "job-1", runType: "background", providerState: "working", sessionId: "session-1", recordPath: "/records/job-1.json" }],
  [{ parentSessionId: "session-1", agentId: "agent-1", transcriptPath: "/transcripts/agent-1.jsonl" }],
);
for (const expected of ["job-1", "background", "working", "agent-1", "/transcripts/agent-1.jsonl"]) {
  assert(claudeMetadata.includes(expected), `Claude metadata omitted ${expected}`);
}
assert(!claudeMetadata.includes("wake") && !claudeMetadata.includes("cancel"), "Claude metadata must not invent controls");

context.app.selected = { id: "session-1", surface: "claude", runtime: {}, status: "idle" };
context.app.history = {
  exchanges: [],
  capabilities: { goal: true },
  readOnly: false,
  timeline: { items: [{ id: "message-2", kind: "message", role: "assistant", text: "Ready" }] },
  claudeRuns: [{ jobId: "job-writable", providerState: "idle" }],
  claudeSubagents: [{ parentSessionId: "session-1", agentId: "agent-writable", transcriptPath: "/transcripts/agent-writable.jsonl" }],
};
context.renderChat();
assert(elements["#chat-body"].innerHTML.includes("job-writable"), "writable Claude sessions should render supplied run observations");
assert(elements["#chat-body"].innerHTML.includes("agent-writable"), "writable Claude sessions should render supplied subagent observations");

const renderGoal = (goal) => {
  context.app.transcriptSignature = null;
  context.app.history.goal = goal;
  elements["#chat-body"].innerHTML = "";
  context.renderChat();
  return elements["#chat-body"].innerHTML;
};
const active = renderGoal({ status: "active", objective: "Investigate", timeUsedSeconds: 125, tokensUsed: 2400, tokenBudget: 5000, createdAt: "2026-10-04T12:01:00Z", updatedAt: "2026-10-04T12:02:00Z" });
assert(active.includes("Pause") && active.includes("Clear") && active.includes("Clear budget"), "active goal should expose pause, clear, and budget actions");
for (const expected of ["Elapsed: 2m 5s", "Tokens: 2k", "Budget: 5k", "Created: 2026-10-04T12:01:00Z", "Updated: 2026-10-04T12:02:00Z"]) assert(active.includes(expected), `active goal omitted ${expected}`);
assert(renderGoal({ status: "paused", objective: "Investigate" }).includes("Resume"), "paused goal should expose resume");
for (const status of ["blocked", "usageLimited", "budgetLimited"]) {
  const html = renderGoal({ status, objective: "Investigate" });
  assert(html.includes(`Needs you: ${context.goalStatusLabel(status)}`), `${status} goal should render attention outside settings`);
  assert(html.indexOf("Needs you:") < html.indexOf("Conversation settings"), `${status} attention should precede collapsed settings`);
}
const complete = renderGoal({ status: "complete", objective: "Investigate" });
assert(!complete.includes("Needs you:"), "complete goal should not render active attention");

const first = [{ id: "call-2", kind: "toolCall", title: "Search", text: "first", callId: "call-8", status: "running" }];
const refreshed = [{ id: "call-2", kind: "toolCall", title: "Search", text: "second", callId: "call-8", status: "complete" }];
assert(context.renderTimeline(first, session).includes("first"), "initial tool item should render");
assert(context.renderTimeline(refreshed, session).includes("second"), "refreshed tool item should render changed content");
assert(!context.renderTimeline(refreshed, session).includes("first"), "refreshed tool item should not retain stale content");
assert.notEqual(
  context.timelineSignature(session, [], null, {}, "", first, [], []),
  context.timelineSignature(session, [], null, {}, "", refreshed, [], []),
  "timeline signature must change when a tool item changes",
);

console.log("dashboard timeline VM tests passed");
