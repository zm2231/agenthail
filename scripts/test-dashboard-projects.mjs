import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";

const source = fs.readFileSync(new URL("../internal/daemon/dashboard/app.js", import.meta.url), "utf8");
const start = source.indexOf("function sessionIsCurrent(");
const end = source.indexOf("function renderSessions()", start);
assert(start >= 0 && end > start);
const widget = { id: "project-widget", displayName: "widget", rootPath: "/work/widget", kind: "git" };
const forge = { id: "h1::project-widget", displayName: "widget", rootPath: "/private/example/secret", kind: "git", host: { id: "h1", name: "forge-agents.tail0000.ts.net", state: "connected" } };
const notes = { id: "project-notes", displayName: "notes", rootPath: "/work/notes", kind: "folder", archived: true };
const at = (hours) => new Date(Date.UTC(2026, 9, 4, 12) - hours * 3600e3).toISOString();
const sessions = [
  ...Array.from({ length: 7 }, (_, index) => ({ id: `w${index}`, name: `w${index}`, surface: "codex", status: "idle", lastActive: at(index), project: widget, current: index === 0 })),
  { id: "busy", name: "busy", surface: "codex", status: "busy", lastActive: at(30), project: widget, current: true },
  { id: "queued", name: "queued", surface: "codex", status: "idle", lastActive: at(31), project: widget, queueCount: 1 },
  { id: "flagged", name: "flagged", surface: "codex", status: "idle", lastActive: at(32), project: widget },
  { id: "lead", name: "lead", surface: "claude", status: "idle", lastActive: at(33), project: widget, subagents: { count: 2, working: 1 } },
  { id: "far", name: "far", surface: "codex", status: "idle", lastActive: at(1), project: forge, host: { id: "h1", name: "forge-agents.tail0000.ts.net", state: "connected" }, current: true },
  { id: "hidden", name: "hidden", surface: "codex", status: "idle", lastActive: at(1), project: widget, current: true, archived: true },
  { id: "kept", name: "kept", surface: "claude", status: "idle", lastActive: at(40), project: notes, archived: true },
  { id: "loose", name: "loose", surface: "claude", status: "idle", lastActive: at(2), current: true },
];
const context = {
  app: { state: { sessions, projects: [widget, forge, notes], attention: [{ sessionId: "flagged" }] }, inboxMode: "current", showArchived: false, expandedProjects: new Set(), openArchivedProjects: false, selected: null },
  labels: { codex: "Codex", claude: "Claude Code" },
  escape: (value) => String(value ?? "").replaceAll("<", "&lt;").replaceAll(">", "&gt;").replaceAll('"', "&quot;"),
  displayName: (session) => session.name, rawDisplayName: (session) => session.name,
  presenceTone: () => "idle", presenceLabel: () => "Ready",
};
vm.runInNewContext(source.slice(start, end), context);
const ids = (list) => list.map((session) => session.id).join(",");
assert.equal(ids(vm.runInContext("scopedSessions()", context)), "w0,busy,far,loose", "Current hides archived sessions");
context.app.inboxMode = "all";
assert(!ids(vm.runInContext("scopedSessions()", context)).includes("hidden"), "All hides archived sessions by default");
context.app.showArchived = true;
assert(ids(vm.runInContext("scopedSessions()", context)).includes("hidden"), "Show archived lists them");
const { html, count } = vm.runInContext('renderProjects("")', context);
assert.equal(count, 2);
const widgetSection = html.slice(0, html.indexOf("</section>"));
for (const id of ["w0", "w4", "busy", "queued", "flagged", "lead"]) assert.match(widgetSection, new RegExp(`data-session="${id}"`), `${id} is visible in a collapsed project`);
assert.doesNotMatch(widgetSection, /data-session="w5"/, "older idle sessions collapse");
assert.match(widgetSection, /Show 2 more/);
assert.doesNotMatch(html, /\/private\/example\/secret|\/work\/widget/, "project headers never show a root path");
assert.match(html, /<span class="host-badge" title="On forge-agents\.tail0000\.ts\.net">forge-agents<\/span>/, "a remote project names its host briefly");
assert.match(html, /data-session="far".*<span class="host-badge" title="On forge-agents\.tail0000\.ts\.net">forge-agents<\/span>/s, "a remote session shows a short host badge");
assert.doesNotMatch(html.slice(0, html.indexOf("</section>")), /host-badge/, "local projects and sessions carry no host badge");
assert.doesNotMatch(html, /data-session="hidden"|data-session="loose"/, "archived sessions and sessions without a project stay out");
assert.match(html, /Archived \(1\)<\/summary>.*data-project-action="project-unarchive" data-project-id="project-notes".*data-session="kept"/s);
console.log("dashboard projects view tests passed");
