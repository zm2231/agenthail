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
assert.match(widgetSection, /data-project-message="project-widget" data-project-name="widget"[^>]*>Message<\/button>/, "an active project offers a message to its conversations");
assert.doesNotMatch(html.slice(html.indexOf("Archived (1)")), /data-project-message/, "an archived project offers no message");
const local = { id: "h0", name: "mac", state: "local" };
const remote = { id: "h1", name: "forge-agents.tail0000.ts.net", state: "connected" };
const leads = (coordinators) => {
  context.app.state.projects = [{ ...widget, coordinators }];
  const rendered = vm.runInContext('renderProjects("")', context).html;
  return rendered.slice(0, rendered.indexOf("</section>"));
};
assert.match(leads([{ host: local, name: "widget-lead", surface: "claude", sessionId: "lead" }]), /<button class="lead-chip" type="button" data-session="lead" title="Open @widget-lead, the project coordinator">@widget-lead<\/button>/, "a started coordinator opens its conversation");
assert.match(leads([{ host: local, name: "widget-lead", surface: "claude" }]), /<span class="lead-chip idle" title="@widget-lead starts on its first message">@widget-lead<\/span>/, "an unstarted coordinator is not clickable");
assert.match(leads([{ host: remote, name: "forge-lead", surface: "codex", sessionId: "h1::s9" }]), /data-session="h1::s9"[^>]*>forge-agents::@forge-lead</, "a paired host's coordinator is named with its host and opens its mirrored session");
const conflict = leads([{ host: local, name: "a<b", surface: "claude", sessionId: "lead" }, { host: remote, name: "forge-lead", surface: "codex" }]);
assert.match(conflict, /<span class="lead-chip conflict" title="Several hosts each have a coordinator: @a&lt;b, forge-agents::@forge-lead\.[^"]*">2 leads<\/span>/, "a conflict names every coordinator and opens none");
assert.doesNotMatch(conflict, /lead-chip"[^>]*data-session/, "a conflict chip is not a button");
assert.doesNotMatch(leads([]), /lead-chip/, "a project without a coordinator shows no chip");
const pickerStart = source.indexOf("function renderStartProjects(");
const pickerEnd = source.indexOf("function syncStartForm()", pickerStart);
assert(pickerStart >= 0 && pickerEnd > pickerStart);
const folder = { value: "~" };
const select = { innerHTML: "", value: "" };
const picker = {
  app: { startOptions: { projects: [{ id: "p-api", displayName: "api", rootPath: "/work/api", kind: "git" }, { id: "p-web", displayName: "web <x>", rootPath: "/work/web", kind: "git" }] } },
  escape: context.escape,
  $: (selector) => (selector === "#new-conversation-project" ? select : { querySelector: () => folder }),
};
vm.runInNewContext(source.slice(pickerStart, pickerEnd), picker);
vm.runInContext("renderStartProjects()", picker);
assert.equal(folder.value, "/work/api", "the most recent project fills an untouched folder");
assert.equal(select.value, "/work/api");
assert.match(select.innerHTML, /<option value="\/work\/web" title="\/work\/web">web &lt;x&gt;<\/option><option value="">Other folder<\/option>$/, "projects are listed by name, then Other folder");
folder.value = "/elsewhere";
vm.runInContext("renderStartProjects()", picker);
assert.equal(select.value, "", "a typed folder keeps Other folder selected");
assert.equal(folder.value, "/elsewhere");
picker.app.startOptions = { projects: [] };
folder.value = "~";
vm.runInContext("renderStartProjects()", picker);
assert.equal(select.value, "");
assert.equal(folder.value, "~", "a host without projects keeps the home folder");
console.log("dashboard projects view tests passed");
