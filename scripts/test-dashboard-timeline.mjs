import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";
import vm from "node:vm";
import * as tools from "../internal/daemon/dashboard/js/tool-activity.js";

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
let currentTools = tools;
const context = {
  tools: new Proxy({}, { get: (_target, name) => currentTools[name] }),
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
  renderLiveTurn: () => {},
  alignTranscriptTop: () => {},
  timeAgo: (value) => `ago:${value}`,
  escape: (value) => String(value ?? "").replace(/[&<>"']/g, (char) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#039;",
  })[char]),
};
await dashboardModules(["markdown", "messages", "timeline-items", "format", "tool-view", "tool-diffs", "tool-runs", "timeline", "claude-metadata", "session-metadata", "chat", "goals", "requests", "background-tasks", "context-usage", "presence"], context);

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
assert.equal((html.match(/data-copy-text="[^"]*">Copy output</g) || []).length, 1, "only tool results with text offer Copy output");

// Rows the reader opens or closes, and output they page, survive a re-render.
const failedRun = [
  { id: "f-call", kind: "toolCall", title: "Bash", text: '{"command":"make"}', callId: "f", status: "failed" },
  { id: "f-result", kind: "toolResult", title: "Bash", text: "boom", callId: "f", status: "failed" },
];
const openOf = (rendered, id) => new RegExp(`data-timeline-item="${id}"[^>]*data-row="[^"]*"( open)?>`).exec(rendered)?.[1] === " open";
assert(openOf(context.renderTimeline(failedRun, session), "f-call"), "a failed call opens on its own");
context.toolRows.set("session-1 f-call", false);
assert(!openOf(context.renderTimeline(failedRun, session), "f-call"), "a failed call the reader closed stays closed");
assert(openOf(context.renderTimeline(failedRun, { ...session, id: "session-2" }), "f-call"), "another session's rows are its own");
context.toolRows.set("session-1 call-1", true);
assert(openOf(context.renderTimeline(items, session), "call-1"), "a call the reader opened stays open");
assert(context.renderTimeline(items, session).includes(">Collapse tool</button></div></details>"), "an open call ends with Collapse tool");
const longRun = Array.from({ length: 8 }, (_, index) => ({ id: `run-${index}`, kind: "toolCall", title: "Bash", text: `{"command":"step ${index}"}` }));
const foldOpen = (rendered) => /<details class="tool-line tool-fold" data-row="session-1 run:run-0"( open)?>/.exec(rendered)?.[1] === " open";
assert(!foldOpen(context.renderTimeline(longRun, session)), "a long run starts folded");
assert(context.renderTimeline(longRun, session).includes(">Collapse activity</button></div></details>"), "an open run ends with Collapse activity");
context.rememberToolRow({ target: { matches: () => true, dataset: { row: "session-1 run:run-0" }, open: true } });
assert(foldOpen(context.renderTimeline(longRun, session)), "the toggle the reader made is kept for the next render");

const longOutput = Array.from({ length: 1200 }, (_, index) => `line ${index + 1}`).join("\n");
const outputRun = [
  { id: "o-call", kind: "toolCall", title: "Bash", text: '{"command":"seq"}', callId: "o" },
  { id: "o-result", kind: "toolResult", title: "Bash", text: longOutput, callId: "o" },
];
const shownOutput = (rendered) => /<pre class="tool-output[^"]*">([^<]*)<\/pre>/.exec(rendered)?.[1].split("\n") || [];
let outputHTML = context.renderTimeline(outputRun, session);
assert(outputHTML.includes(">Show all 1200 lines<"), "long output offers all of its lines");
assert.deepEqual(shownOutput(outputHTML).at(-1), "line 24", "long output starts as a 24-line preview");
assert(!outputHTML.includes(">Show less<"), "a preview offers no Show less");
context.toolRows.set("session-1 output:o-result", 500);
outputHTML = context.renderTimeline(outputRun, session);
assert.equal(shownOutput(outputHTML).length, 500, "a page shows 500 lines");
assert.equal((outputHTML.match(/>Show 500 more lines</g) || []).length, 2, "the next page is offered above and below the output");
assert(outputHTML.includes('data-output-show="0" data-reveal>Show less'), "Show less follows the output");
context.toolRows.set("session-1 output:o-result", 1000);
assert(context.renderTimeline(outputRun, session).includes(">Show 200 more lines<"), "the last page offers what is left");
context.toolRows.set("session-1 output:o-result", 1500);
outputHTML = context.renderTimeline(outputRun, session);
assert(shownOutput(outputHTML).length === 1200 && !outputHTML.includes("more lines<"), "the whole output offers no more");
context.toolRows.set("session-1 output:result-1", 500);
assert(!context.renderTimeline(items, session).includes(">Show less<"), "output shorter than its preview never pages");

// Paging replaces only the output block, and Show less at the bottom brings the output's top back.
const revealed = [];
const pagedBlock = { dataset: { row: "session-1 output:o-result", timelineItem: "o-result" }, outerHTML: "" };
context.CSS = { escape: (value) => value };
elements["#chat-body"].querySelector = (selector) => (selector.includes("output:o-result") ? { scrollIntoView: (options) => revealed.push(options) } : null);
context.app.history = { timeline: { items: outputRun } };
context.app.selected = session;
const button = (data, attributes = {}) => ({ dataset: data, closest: () => pagedBlock, hasAttribute: (name) => name in attributes });
context.showToolOutput(button({ outputShow: "1000" }));
assert.equal(context.toolRows.get("session-1 output:o-result"), 1000);
assert(pagedBlock.outerHTML.startsWith('<div class="tool-result"') && shownOutput(pagedBlock.outerHTML).length === 1000, "the block is re-rendered in place");
assert.equal(revealed.length, 0, "paging forward keeps the reader's place");
context.showToolOutput(button({ outputShow: "0" }, { "data-reveal": "" }));
assert.equal(context.toolRows.get("session-1 output:o-result"), 0);
assert.equal(JSON.stringify(revealed), JSON.stringify([{ block: "nearest" }]), "Show less from the bottom scrolls the output into view");

// An edit shows what it changed, and the run's chips open that file's diff in place.
const editRun = [
  { id: "e-1", kind: "toolCall", title: "Edit", text: JSON.stringify({ file_path: "/w/a.swift", old_string: "let x = 1", new_string: "let y = 1" }) },
  { id: "e-2", kind: "toolCall", title: "Write", text: JSON.stringify({ file_path: "/w/b.md", content: "one\ntwo\n" }) },
  { id: "e-3", kind: "toolCall", title: "Edit", text: JSON.stringify({ file_path: "/w/a.swift", old_string: "a", new_string: "b" }) },
];
let editHTML = context.renderTimeline(editRun, session);
assert(editHTML.includes('<div class="diff-line removed"><span class="diff-sign" aria-hidden="true">−</span><span class="diff-text"><span class="sr-only">Removed: </span>let <mark>x</mark> = 1</span></div>'), "an edit line shows its diff with the changed word marked");
assert(!editHTML.includes('class="tool-input"'), "an edit shows its diff instead of its raw input");
assert(editHTML.includes('data-chip="/w/a.swift" aria-expanded="false"') && editHTML.includes('aria-label="a.swift, 2 added, 2 removed"'), "a file edited twice is one chip with both edits counted");
const chipBlock = { dataset: { block: "session-1 chips:e-1" }, outerHTML: "" };
context.toggleFileChip({ dataset: { chip: "/w/a.swift" }, closest: () => chipBlock });
assert(chipBlock.outerHTML.includes('data-chip="/w/a.swift" aria-expanded="true"') && (chipBlock.outerHTML.match(/class="tool-diff"/g) || []).length === 1, "a chip opens its file's diff in place");
assert(chipBlock.outerHTML.includes('<div class="diff-note">⋯</div>'), "the file's two edits show as two hunks");
editHTML = context.renderTimeline(editRun, session);
assert(editHTML.includes('data-chip="/w/a.swift" aria-expanded="true"'), "an open chip stays open across renders");
context.toggleFileChip({ dataset: { chip: "/w/a.swift" }, closest: () => chipBlock });
assert(!chipBlock.outerHTML.includes('class="tool-diff"'), "the chip closes again");
const bigWrite = [{ id: "w-big", kind: "toolCall", title: "Write", text: JSON.stringify({ file_path: "/w/big.txt", content: Array.from({ length: 900 }, (_, index) => `row ${index}`).join("\n") }) }];
const bigHTML = context.renderTimeline(bigWrite, session);
assert.equal((bigHTML.match(/<div class="diff-line /g) || []).length, 400, "a long diff lays out 400 rows at first");
assert(bigHTML.includes('data-diff-show="800">Show 400 more lines'), "and offers the next 400");
const diffBlock = { dataset: { block: "session-1 diff:w-big:/w/big.txt" }, outerHTML: "" };
context.showToolDiff({ dataset: { diffShow: "800" }, closest: () => diffBlock });
assert.equal((diffBlock.outerHTML.match(/<div class="diff-line /g) || []).length, 800, "Show more adds a page in place");
assert(diffBlock.outerHTML.includes(">Show 100 more lines<"), "the last page offers what is left");

// An unchanged call's diffs are worked out once, however often the transcript renders.
const patchText = `*** Begin Patch\n*** Update File: big.txt\n@@\n${Array.from({ length: 300 }, (_, index) => `-old ${index}\n+new ${index}`).join("\n")}\n*** End Patch`;
const patchRun = Array.from({ length: 7 }, (_, index) => ({ id: `p-${index}`, kind: "toolCall", title: "apply_patch", text: index ? JSON.stringify({ command: `step ${index}` }) : patchText }));
let derived = 0;
const realTools = tools;
currentTools = { ...realTools, toolDiffs: (call) => { derived += 1; return realTools.toolDiffs(call); }, toolLine: (call, diffs) => { if (call.title === "apply_patch") assert(diffs, "a patch's line reuses its cached diffs"); return realTools.toolLine(call, diffs); } };
context.renderTimeline(patchRun, session);
const firstRender = derived;
context.renderTimeline(patchRun, session);
currentTools = realTools;
assert(firstRender > 0 && derived === firstRender, `a second render derives no diffs again (${firstRender} then ${derived - firstRender})`);

// Collapse from the bottom closes the row and scrolls its header into view.
const summaryRevealed = [];
const row = { open: true, dataset: { row: "session-1 run:run-0" }, querySelector: () => ({ scrollIntoView: (options) => summaryRevealed.push(options) }) };
context.collapseToolRow({ closest: () => row });
assert(!row.open && context.toolRows.get("session-1 run:run-0") === false, "Collapse closes the row and remembers it");
assert.equal(JSON.stringify(summaryRevealed), JSON.stringify([{ block: "nearest" }]), "Collapse scrolls the row's header into view");
const longReply = Array.from({ length: 40 }, (_, index) => `- synthetic line ${index + 1} of a long reply`).join("\n");
const conversation = context.renderTimeline([
  { id: "m-user", kind: "message", role: "user", text: "Synthetic **request**" },
  { id: "m-handoff", kind: "message", role: "peer", sender: "@peer", text: "Synthetic handoff", agent: { direction: "in", kind: "peer", peer: "@peer", summary: "Synthetic handoff" } },
  { id: "m-peer", kind: "message", role: "peer", sender: "@peer", text: "Synthetic peer note", agent: { direction: "in", kind: "peer", peer: "@peer", summary: "Synthetic peer note" } },
  { id: "m-agent", kind: "message", role: "assistant", text: longReply },
], session);
assert(conversation.includes('<details class="timeline-item agent-fold"') && conversation.includes("2 messages from @peer"), "consecutive agent messages fold into one row");
assert(!conversation.includes("[from"), "agent messages carry no envelope");
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
for (const expected of ["job-1", "background", "working", "Subagent agent-1", "1 subagent in all"]) {
  assert(claudeMetadata.includes(expected), `Claude metadata omitted ${expected}`);
}
assert(!claudeMetadata.includes("/transcripts/agent-1.jsonl"), "a subagent row shows what it does, not its transcript path");

const subagentLinks = Array.from({ length: 12 }, (_, index) => ({
  parentSessionId: "session-1",
  agentId: `agent-${String(index).padStart(2, "0")}`,
  working: index >= 10,
  lastActive: new Date(Date.UTC(2026, 9, 23, 12, 0) - index * 60000).toISOString(),
  transcriptPath: `/transcripts/agent-${index}.jsonl`,
  ...(index === 0 ? { description: "Review the parser", agentType: "general-purpose" } : {}),
}));
const rowsOf = (html) => [...html.matchAll(/<li><span>([^<]*)<\/span>/g)].map((match) => match[1]);
const paged = context.renderClaudeMetadata([], subagentLinks, 30);
assert.deepEqual(rowsOf(paged), ["Subagent agent-10", "Subagent agent-11", "Review the parser", ...[1, 2, 3, 4, 5, 6, 7].map((index) => `Subagent agent-0${index}`)], "working subagents come first, then the most recent");
assert(paged.includes("2 working, 30 subagents in all") && paged.includes("data-subagents-all") && paged.includes("Show 20 earlier"), "the server total counts the earlier subagents");
assert(paged.includes("· <span>general-purpose</span>") && !paged.includes("/transcripts/"), "a described subagent shows its type and no path");
const older = context.renderClaudeMetadata([], subagentLinks);
assert(rowsOf(older).length === 10 && older.includes("Show 2 earlier"), "a host without a total sent every subagent; the dashboard collapses them itself");
const fetched = [];
context.fetch = (url) => { fetched.push(url); return new Promise(() => {}); };
context.app.allSubagents = false;
context.loadSessionMetadata("session 1", {});
context.app.allSubagents = true;
context.loadSessionMetadata("session 1", {});
assert.deepEqual(fetched, ["/api/session-metadata?id=session%201", "/api/session-metadata?id=session%201&subagents=all"], "only an expanded list asks for every subagent");
const expanded = context.renderClaudeMetadata([], subagentLinks, 12, true);
assert(rowsOf(expanded).length === 12 && !expanded.includes("data-subagents-all") && expanded.includes("<details class=\"session-details\" open>"), "an expanded list shows every subagent and stays open");
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
assert(elements["#chat-body"].innerHTML.includes("Subagent agent-wr"), "writable Claude sessions should render supplied subagent observations");

context.app.transcriptSignature = null;
context.app.history = {
  exchanges: [{ user: "Synthetic fallback request", assistant: "Synthetic fallback reply" }, { user: "Synthetic fallback handoff", origin: "peer", sender: "@peer", assistant: "" }],
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
const pending = (sessionState, ...after) => context.renderTimeline([
  { id: "p1", kind: "toolCall", title: "Bash", text: JSON.stringify({ command: "sleep 600", timeout: 600 }), callId: "p1", turnId: "t9" },
  ...after,
], { ...session, ...sessionState });
assert(pending({ status: "busy" }).includes('<p class="timeline-empty">Running</p>'), "a call in a working session's open turn is running");
assert(pending({ status: "idle", waitingOn: { kind: "approval", id: "w" } }).includes('<p class="timeline-empty">Running</p>'), "a call waiting on approval is running");
assert(pending({ status: "busy" }, { id: "x", kind: "done", turnId: "t9", status: "cancelled" }).includes("Interrupted before a result"), "a call whose turn was cancelled says so");
assert(pending({ status: "busy" }, { id: "x", kind: "done", turnId: "t8", status: "cancelled" }).includes('<p class="timeline-empty">Running</p>'), "another turn's end does not interrupt a running call");
assert(pending({ status: "idle" }).includes("No result recorded"), "a quiet session's call with no end recorded claims nothing more");
assert(!pending({ status: "busy" }).includes("No result yet"), "no surface guesses that a result is coming");
assert(pending({ status: "busy" }).includes("$ sleep 600\ntimeout: 600"), "input reads as a command and its fields, not JSON");
const split = pending({ status: "busy" }, { id: "n1", kind: "event", title: "Task notification", text: "done", turnId: "t9" }, { id: "p1-r", kind: "toolResult", title: "Bash result", text: "slept", callId: "p1", turnId: "t9" });
assert(split.includes("Result recorded further down") && split.includes("slept") && !split.includes(">Running<"), "a result that lands after another item is pointed to, not called missing");
{
  let attempts = 0;
  const box = {};
  const modules = await dashboardModules(["tool-loader"], box, {
    dynamicImport: async (_specifier, _parent, context) => {
      attempts += 1;
      if (attempts === 1) throw new Error("offline");
      const module = new vm.SyntheticModule(["ok"], function () { this.setExport("ok", true); }, { context });
      await module.link(() => {});
      await module.evaluate();
      return module;
    },
  });
  await assert.rejects(box.loadTools(), /offline/, "a failed import reaches load's error handling");
  await box.loadTools();
  assert.equal(attempts, 2, "the next load retries the import");
  assert.equal(modules["tool-loader"].tools.ok, true, "a successful retry installs the tool renderer");
  await box.loadTools();
  assert.equal(attempts, 2, "a successful import is reused");
}
const written = context.renderTimeline(call("w1", "Write", { file_path: "/work/One.swift", content: "one\n" }), session);
assert(written.includes('<span>One.swift</span><span class="added">+1</span>'), `a final newline does not count as another line: ${written.match(/file-chips.*?<\/div>/)?.[0]}`);
const long = context.renderTimeline(Array.from({ length: 7 }, (_, index) => call(`b${index}`, "Bash", { command: `go test ./case${index}` })).flat(), session);
assert(long.includes('class="tool-line tool-fold"') && long.includes("Ran 7 commands"), "a run of more than six calls folds into one summary line");
const six = context.renderTimeline(Array.from({ length: 6 }, (_, index) => call(`s${index}`, "Bash", { command: `go test ./case${index}` })).flat(), session);
assert(!six.includes("tool-fold"), "six calls stay as lines");
const authors = context.renderTimeline([
  { id: "h1", kind: "message", role: "peer", sender: "@builder", text: "Branch is ready", agent: { direction: "in", kind: "peer", peer: "@builder", summary: "Branch is ready" } },
  { id: "h2", kind: "message", role: "user", text: "Ship it", timestamp: "2026-10-23T10:00:00Z" },
  { id: "e1", kind: "event", title: "Context compacted", text: "38k tokens freed" },
], session);
assert(authors.includes('class="timeline-item agent-message"') && authors.includes('<span class="agent-chip">@builder</span>'), "a handed-off message reads as another agent's");
const raw = context.renderTimeline([{ id: "r1", kind: "message", role: "user", text: "[from @builder] quoted" }], session);
assert(raw.includes('class="turn user"') && raw.includes("[from @builder] quoted"), "the dashboard reads no envelope itself");
assert(authors.includes('class="turn user"'), "a typed message reads as yours");
assert(authors.includes('class="timeline-item timeline-divider"') && authors.includes("Context compacted: 38k tokens freed"), "an event is a divider, not an unknown card");
assert.equal(context.durationClock("1h2m3s"), "1:02:03");
assert.equal(context.durationClock("12s"), "0:12");
assert.equal(context.durationClock("about a minute"), "about a minute");

console.log("dashboard timeline VM tests passed");
