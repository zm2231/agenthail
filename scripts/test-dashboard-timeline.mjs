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
const presenceStart = source.indexOf("function statusLabel");
const presenceEnd = source.indexOf("function conversationMeta", presenceStart);
assert(markdownStart >= 0 && markdownEnd > markdownStart, "markdown renderer boundary missing");
assert(messageStart >= 0 && messageEnd > messageStart, "message renderer boundary missing");
assert(rendererStart >= 0 && rendererEnd > rendererStart, "timeline renderer boundary missing");
assert(chatEnd > chatStart, "chat renderer boundary missing");
assert(goalEnd > goalStart, "goal renderer boundary missing");
assert(presenceStart >= 0 && presenceEnd > presenceStart, "presence label boundary missing");

const elements = {
  "#chat-actions": { innerHTML: "" },
  "#message": { disabled: false, placeholder: "" },
  "#send": { disabled: false },
  "#composer-note": { textContent: "" },
  "#chat-subtitle": { textContent: "" },
  "#thread-count": { textContent: "" },
  "#request-panel": { hidden: false, innerHTML: "stale" },
  "#background-panel": { hidden: false, innerHTML: "stale" },
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
  app: { slashCommands: [], pendingEntryScroll: false, transcriptSignature: null },
  labels: { claude: "Claude Code", codex: "Codex" },
  $: (selector) => elements[selector],
  mobileConversationView: () => false,
  syncComposerAction: () => {},
  renderSlashMenu: () => {},
  conversationMeta: () => "Claude Code",
  displayName: (session) => session.name || session.id,
  remoteHostName: () => "",
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
${source.slice(presenceStart, presenceEnd)}
globalThis.presenceLabel = presenceLabel;
globalThis.presenceTone = presenceTone;
globalThis.renderBackgroundTasks = renderBackgroundTasks;
globalThis.renderRequests = renderRequests;
globalThis.renderRequestCard = renderRequestCard;
globalThis.renderChat = renderChat;
globalThis.goalStatusLabel = goalStatusLabel;
globalThis.contextBreakdown = contextBreakdown;`, context);

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
for (const expected of ["Read the report", "report.md", "report contents", 'data-call-id="call-7"', 'data-status="running"', 'data-status="complete"', "attachment-1", "Shortened by the host", "Future event", "futureEvent"]) {
  assert(html.includes(expected), `timeline omitted ${expected}`);
}
for (const debug of ["Title: ", "Call: ", "Status: ", "2026-10-04T12:00:03Z", "Body reference", "blob://body-1"]) {
  assert(!html.includes(debug), `timeline shows debug metadata ${debug}`);
}
for (const item of [
  { kind: "message", role: "user", text: "typed" },
  { kind: "message", role: "assistant", text: "reply" },
  { kind: "message", role: "peer", sender: "@peer", text: "note" },
  { kind: "instructions", text: "context" },
  { kind: "attachment", text: "A chart", attachment: { id: "attachment-2", mediaType: "image/png" } },
  { kind: "notice", title: "Usage limit", text: "Paused" },
  { kind: "toolCall", title: "Read", text: "report.md" },
  { kind: "toolResult", title: "Read result", text: "contents" },
  { kind: "reasoning", text: "thinking" },
  { kind: "futureEvent", title: "Future", text: "body" },
]) {
  const rendered = context.renderTimeline([{ id: `short-${item.kind}-${item.role || ""}`, truncated: true, ...item }], session);
  assert(rendered.includes("Shortened by the host"), `a shortened ${item.kind} ${item.role || ""} record omits the note`);
}
assert(html.includes("<details"), "tool and reasoning items should be collapsible");
assert(html.includes('data-copy-text="report contents"'), "tool results should offer their raw text for copying");
assert.equal((html.match(/class="soft-button copy-output"/g) || []).length, 1, "only tool results with text offer Copy output");
const longReply = Array.from({ length: 40 }, (_, index) => `- synthetic line ${index + 1} of a long reply`).join("\n");
const conversation = context.renderTimeline([
  { id: "m-user", kind: "message", role: "user", text: "Synthetic **request**" },
  { id: "m-handoff", kind: "message", role: "user", text: "[from @peer] Synthetic handoff" },
  { id: "m-peer", kind: "message", role: "peer", sender: "@peer", text: "Synthetic peer note" },
  { id: "m-agent", kind: "message", role: "assistant", text: longReply },
], session);
assert(conversation.includes("synthetic line 40 of a long reply") && !conversation.includes("Show full message"), "long replies render in full without a crop");
assert(conversation.includes(`data-copy-text="${longReply}"`), "Copy on a reply carries its whole Markdown");
for (const text of ["Synthetic **request**", "Synthetic handoff", "Synthetic peer note"]) assert(conversation.includes(`data-copy-text="${text}"`), `Copy omitted ${text}`);
assert.equal((conversation.match(/class="turn-action" type="button" data-copy-text=/g) || []).length, 4, "every message offers Copy");
assert.equal((conversation.match(/data-edit-message=/g) || []).length, 1, "only your own typed message offers Edit");
assert(conversation.includes('data-edit-message="Synthetic **request**"'), "Edit carries the message text");
const injected = context.renderTimeline([
  { id: "i-codex", kind: "instructions", title: "Instructions", text: "# AGENTS.md instructions for /work" },
  { id: "i-developer", kind: "message", role: "developer", text: "<canon-recall>recalled</canon-recall>" },
  { id: "i-system", kind: "message", role: "system", text: "System context" },
], session);
assert.equal((injected.match(/<details class="timeline-item timeline-instructions timeline-collapsible"/g) || []).length, 3, "injected context renders as collapsed instructions");
assert.equal((injected.match(/<summary>Instructions<\/summary>/g) || []).length, 3, "each injection is labelled Instructions");
assert(injected.includes("AGENTS.md instructions") && injected.includes("System context"), "instructions expand to their text");
assert(!injected.includes("data-copy-text") && !injected.includes('class="turn-action'), "instructions are not shown as a message from anyone");
const quoted = context.renderTimeline([{ id: "result-2", kind: "toolResult", title: "Result", text: `say "hi" <b>` }], session);
assert(quoted.includes('data-copy-text="say &quot;hi&quot; &lt;b&gt;"'), "copied text must be attribute-escaped");
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
  capabilities: { goal: true, goalSet: true, goalStatus: true },
  readOnly: false,
  timeline: { items: [{ id: "message-2", kind: "message", role: "assistant", text: "Ready" }] },
  claudeRuns: [{ jobId: "job-writable", providerState: "idle" }],
  claudeSubagents: [{ parentSessionId: "session-1", agentId: "agent-writable", transcriptPath: "/transcripts/agent-writable.jsonl" }],
};
context.renderChat();
assert(elements["#chat-body"].innerHTML.includes("job-writable"), "writable Claude sessions should render supplied run observations");
assert(elements["#request-panel"].hidden && elements["#request-panel"].innerHTML === "", "a session that takes no answers here shows no request panel");
assert(elements["#chat-body"].innerHTML.includes("agent-writable"), "writable Claude sessions should render supplied subagent observations");

context.app.transcriptSignature = null;
context.app.history = {
  exchanges: [{ user: "Synthetic fallback request", assistant: "Synthetic fallback reply" }, { user: "[from @peer] Synthetic fallback handoff", assistant: "" }],
  capabilities: {},
  readOnly: false,
  timeline: { items: [] },
};
context.renderChat();
const fallback = elements["#chat-body"].innerHTML;
for (const text of ["Synthetic fallback request", "Synthetic fallback reply", "Synthetic fallback handoff"]) assert(fallback.includes(`data-copy-text="${text}"`), `exchange fallback Copy omitted ${text}`);
assert.equal((fallback.match(/data-edit-message=/g) || []).length, 1, "exchange fallback offers Edit only on your typed message");
assert(fallback.includes('data-edit-message="Synthetic fallback request"'), "exchange fallback Edit carries the message text");
context.app.history = {
  exchanges: [],
  capabilities: { goal: true, goalSet: true, goalStatus: true },
  readOnly: false,
  timeline: { items: [{ id: "message-2", kind: "message", role: "assistant", text: "Ready" }] },
};

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
context.app.history.capabilities = { goal: false, goalSet: true, goalStatus: true };
const claudeGoal = renderGoal({ status: "active", objective: "Investigate" });
assert(claudeGoal.includes("Clear") && !claudeGoal.includes("Pause") && !claudeGoal.includes("budget"), "a goal set by command offers clear but no pause or budget");
context.app.history.capabilities = { goal: true, goalSet: true, goalStatus: true };

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

const full = context.contextBreakdown({ usedTokens: 86400, contextWindow: 200000, cumulativeTokens: 312000, compactionCount: 1, reclaimedTokens: 42000, inputTokens: 83000, cachedInputTokens: 60000, outputTokens: 3400, reasoningOutputTokens: 900 });
assert.equal(JSON.stringify(full.map(([label]) => label)), JSON.stringify(["Used tokens", "Context window", "Input", "Cached input", "Output", "Reasoning output", "Cumulative tokens", "Compactions", "Tokens reclaimed"]), "every reported figure appears in order");
assert.equal(JSON.stringify(context.contextBreakdown({ usedTokens: 0, contextWindow: 0, compactionCount: 0 }).map(([label, value]) => `${label}=${value}`)), JSON.stringify(["Used tokens=0", "Context window=Unavailable", "Compactions=0"]), "unreported figures are left out and an unknown window says so");
assert.equal(context.contextBreakdown({ usedTokens: 1, contextWindow: 9, compactionCount: 0, windowEstimated: true })[1][0], "Estimated window", "an estimated window is labeled");
assert.equal(context.contextBreakdown({ usedTokens: 1, contextWindow: 9, compactionCount: 0, contextWindowSource: "configured" })[1][0], "Configured window", "a configured window is labeled");

assert(elements["#background-panel"].hidden && elements["#background-panel"].innerHTML === "", "a session with no background work shows no background panel");
const backgroundSession = {
  id: "session-bg", surface: "claude", status: "idle",
  backgroundTasks: { running: true, source: "transcript", tasks: [
    { id: "b1", kind: "shell", description: "go test ./...", since: "2026-10-20T12:00:00Z" },
    { id: "m1", kind: "monitor", description: "file watcher", ambient: true },
  ] },
};
assert.equal(context.presenceLabel(backgroundSession), "Waiting on background work", "a session between turns with background work says so");
assert.equal(context.presenceTone(backgroundSession), "background", "background work has its own tone");
assert.equal(context.presenceLabel({ ...backgroundSession, status: "busy" }), "Working", "a turn in progress still reads as working");
assert.equal(context.presenceLabel({ ...backgroundSession, waitingOn: { kind: "approval", id: "r1", answerable: true } }), "Waiting on you", "a question outranks background work");
assert.notEqual(context.presenceLabel({ ...backgroundSession, backgroundTasks: { running: false, source: "host", tasks: [{ id: "m1", kind: "monitor", ambient: true }] } }), "Waiting on background work", "ambient tasks alone do not hold the session");
context.renderBackgroundTasks(backgroundSession);
const backgroundPanel = elements["#background-panel"].innerHTML;
assert(!elements["#background-panel"].hidden, "background work shows its panel");
for (const expected of ["1 background task running", "go test ./...", "file watcher", "ambient", "Read from the transcript."]) assert(backgroundPanel.includes(expected), `background panel omitted ${expected}`);
context.renderBackgroundTasks({ ...backgroundSession, backgroundTasks: { running: true, source: "status" } });
assert(elements["#background-panel"].innerHTML.includes("Background work running") && elements["#background-panel"].innerHTML.includes("not which one"), "a shell reported without a list says what is known");
const notice = context.renderTimeline([{ id: "limit-1", kind: "notice", title: "Usage limit reached", text: "You've hit your session limit <b>", status: "usage_limit" }], session);
assert(notice.includes("timeline-notice") && notice.includes("Usage limit reached") && notice.includes("&lt;b&gt;") && !notice.includes("turn-action"), "a notice renders as an escaped status line, not as the agent's message");
assert(context.renderTimeline([{ id: "limit-2", kind: "notice", title: "Usage limit reached", text: "limit", status: "usage_limit", resetsAt: "2026-10-21T11:00:00Z" }], session).includes("Resets "), "a usage-limit notice names when it resets");
assert.equal(context.renderTimeline([{ id: "done-1", kind: "done", status: "failed", text: "limit" }], session), "", "a turn's end renders nothing");

context.app.selected = { id: "session-dialog", name: "builder", surface: "claude", status: "busy", runtime: {}, waitingOn: { kind: "dialog", id: "session-dialog:dialog:1" } };
context.app.history = { capabilities: { respond: true } };
context.app.requests = { requests: [{ id: "stale", kind: "approval", title: "Run ls" }] };
context.renderRequests();
assert(elements["#request-panel"].innerHTML.includes("builder has a dialog open in its terminal") && !elements["#request-panel"].hidden, "a dialog says where to settle it even in a session that takes answers");
assert(!elements["#request-panel"].innerHTML.includes("data-request") && !elements["#request-panel"].innerHTML.includes("Run ls"), "a dialog shows no request still listed from before it opened");
context.app.requests = { requests: [] };
context.app.selected.waitingOn = { kind: "approval", id: "session-dialog:approval:1", answerable: true };
context.renderRequests();
assert(elements["#request-panel"].hidden, "an approval with no request yet shows no card until its request is read");
context.app.requests = { requests: [{ id: "live", kind: "approval", title: "Run ls" }] };
context.renderRequests();
assert(elements["#request-panel"].innerHTML.includes("data-request-respond=\"allow\"") && elements["#request-panel"].innerHTML.includes("Run ls"), "an approval shows its request card");
const formCard = context.renderRequestCard({
  id: "e1",
  kind: "input",
  elicitation: {
    server: "deploy",
    message: "Where should this deploy?",
    mode: "form",
    fields: [
      { name: "env", title: "Environment", type: "string", required: true, multiSelect: false, options: [{ value: "staging", label: "Staging" }, { value: "prod", label: "Production" }] },
      { name: "replicas", title: "Replicas", type: "integer", required: false, multiSelect: false, default: 2 },
      { name: "notify", title: "Notify", type: "boolean", required: false, multiSelect: false },
      { name: "regions", title: "Regions", type: "string", required: false, multiSelect: true, options: [{ value: "us", label: "US" }] },
    ],
  },
});
for (const expected of ['data-request-input="e1"', "deploy asks for input", "Where should this deploy?", "Environment *", '<option value="prod">Production</option>', 'type="number" step="1" name="field-replicas" value="2"', 'type="checkbox" name="field-notify"', 'type="checkbox" name="field-regions" value="us"', 'data-request-respond="deny"']) {
  assert(formCard.includes(expected), `an MCP form card shows ${expected}`);
}
assert(!formCard.includes('data-request-respond="allow"'), "an MCP form is submitted with its fields, not allowed");
const linkCard = context.renderRequestCard({ id: "e2", kind: "input", elicitation: { server: "docs", message: "Sign in", mode: "url", url: "https://example.com/auth" } });
assert(linkCard.includes('href="https://example.com/auth"') && linkCard.includes('rel="noopener noreferrer"') && linkCard.includes('data-request-respond="allow"'), "an MCP link card opens the link and continues");
const scriptLink = context.renderRequestCard({ id: "e3", kind: "input", elicitation: { server: "docs", message: "Sign in", mode: "url", url: "javascript:alert(1)" } });
assert(!scriptLink.includes("href="), "a link that is not http or https is shown, not linked");

const call = (id, title, input, result = "ok", status = "completed") => [
  { id, kind: "toolCall", title, text: JSON.stringify(input), callId: id },
  { id: `${id}-r`, kind: "toolResult", title: `${title} result`, text: result, callId: id, status },
];
const run = context.renderTimeline([
  ...call("t1", "Grep", { pattern: "Collapse activity", path: "/work/native" }),
  ...call("t2", "Bash", { command: "swift test --filter H" }, "Test Case 'H' failed", "failed"),
  ...call("t3", "Edit", { file_path: "/work/App.swift", old_string: "a\nb\nc", new_string: "a\nB\nc\nd\ne" }),
  ...call("t4", "Edit", { file_path: "/work/App.swift", old_string: "x", new_string: "y" }),
  { id: "m1", kind: "message", role: "assistant", text: "Fixed.", timestamp: "2026-10-23T10:09:00Z" },
  { id: "d1", kind: "event", title: "Turn duration", text: "3m13s", timestamp: "2026-10-23T10:10:00Z" },
], session);
assert.equal((run.match(/class="timeline-item tool-line/g) || []).length, 4, "each call is one line, with its result inside it");
assert(run.includes('<span class="tool-verb">Search</span><span class="tool-object code">Collapse activity</span>'), "a search line names its pattern");
assert(/class="timeline-item tool-line failed"[^>]* open>/.test(run), "a failed call is marked and opens itself");
assert(run.includes('<span class="tool-failed">failed</span>'), "a failed call says so");
assert(run.includes('<span>App.swift</span><span class="added">+4</span><span class="removed">−2</span>'), `edits to one file merge into one chip with line counts: ${run.match(/file-chips.*?<\/div>/)?.[0]}`);
assert(run.includes("Finished 3:13 · "), "the reply that ends a turn shows how long it took and when");
assert(!run.includes("timeline-annotation"), "the duration joins the reply rather than standing alone");
assert(/<footer class="turn-footer"><span class="turn-status">Finished 3:13[^<]*<\/span><span class="turn-actions"><button class="turn-action" type="button" data-copy-text="Fixed.">Copy<\/button>/.test(run), "Copy sits on the footer line beside the time");
const written = context.renderTimeline(call("w1", "Write", { file_path: "/work/One.swift", content: "one\n" }), session);
assert(written.includes('<span>One.swift</span><span class="added">+1</span>'), `a final newline does not count as another line: ${written.match(/file-chips.*?<\/div>/)?.[0]}`);
const long = context.renderTimeline(Array.from({ length: 7 }, (_, index) => call(`b${index}`, "Bash", { command: `go test ./case${index}` })).flat(), session);
assert(long.includes('class="tool-line tool-fold"') && long.includes("Ran 7 commands"), "a run of more than six calls folds into one summary line");
const six = context.renderTimeline(Array.from({ length: 6 }, (_, index) => call(`s${index}`, "Bash", { command: `go test ./case${index}` })).flat(), session);
assert(!six.includes("tool-fold"), "six calls stay as lines");
const authors = context.renderTimeline([
  { id: "h1", kind: "message", role: "user", text: "[from @builder] Branch is ready" },
  { id: "h2", kind: "message", role: "user", text: "Ship it", timestamp: "2026-10-23T10:00:00Z" },
  { id: "e1", kind: "event", title: "Context compacted", text: "38k tokens freed" },
], session);
assert(authors.includes('class="turn peer"') && authors.includes("From @builder"), "a handed-off message reads as another agent's");
assert(authors.includes('class="turn user"'), "a typed message reads as yours");
assert(authors.includes('class="timeline-item timeline-divider"') && authors.includes("Context compacted: 38k tokens freed"), "an event is a divider, not an unknown card");
assert.equal(context.durationClock("1h2m3s"), "1:02:03");
assert.equal(context.durationClock("12s"), "0:12");
assert.equal(context.durationClock("about a minute"), "about a minute");

console.log("dashboard timeline VM tests passed");
