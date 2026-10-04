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
assert(markdownStart >= 0 && markdownEnd > markdownStart, "markdown renderer boundary missing");
assert(messageStart >= 0 && messageEnd > messageStart, "message renderer boundary missing");
assert(rendererStart >= 0 && rendererEnd > rendererStart, "timeline renderer boundary missing");

const context = {
  app: { expandedTurns: new Set() },
  labels: { claude: "Claude Code", codex: "Codex" },
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
globalThis.renderClaudeMetadata = renderClaudeMetadata;`, context);

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
