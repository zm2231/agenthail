import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";

const source = fs.readFileSync(new URL("../internal/daemon/dashboard/app.js", import.meta.url), "utf8");
const start = source.indexOf("function sessionIsCurrent(");
const end = source.indexOf("function renderSessions()", start);
assert(start >= 0 && end > start);
const widget = { id: "project-widget", displayName: "widget", rootPath: "/work/widget", kind: "git" };
const vps = { id: "h1::project-widget", displayName: "widget", rootPath: "/private/example/secret", kind: "git", host: { id: "h1", name: "vps.example.ts.net", state: "connected" } };
const notes = { id: "project-notes", displayName: "notes", rootPath: "/work/notes", kind: "folder", archived: true };
const at = (hours) => new Date(Date.UTC(2026, 9, 4, 12) - hours * 3600e3).toISOString();
const sessions = [
  ...Array.from({ length: 7 }, (_, index) => ({ id: `w${index}`, name: `w${index}`, surface: "codex", status: "idle", lastActive: at(index), project: widget, current: index === 0 })),
  { id: "busy", name: "busy", surface: "codex", status: "busy", lastActive: at(30), project: widget, current: true },
  { id: "queued", name: "queued", surface: "codex", status: "idle", lastActive: at(31), project: widget, queueCount: 1 },
  { id: "flagged", name: "flagged", surface: "codex", status: "idle", lastActive: at(32), project: widget },
  { id: "lead", name: "lead", surface: "claude", status: "idle", lastActive: at(33), project: widget, subagents: { count: 2, working: 1 } },
  { id: "far", name: "far", surface: "codex", status: "idle", lastActive: at(1), project: vps, host: { id: "h1", name: "vps.example.ts.net", state: "connected" }, current: true },
  { id: "hidden", name: "hidden", surface: "codex", status: "idle", lastActive: at(1), project: widget, current: true, archived: true },
  { id: "kept", name: "kept", surface: "claude", status: "idle", lastActive: at(40), project: notes, archived: true },
  { id: "loose", name: "loose", surface: "claude", status: "idle", lastActive: at(2), current: true },
];
const context = {
  app: { state: { sessions, projects: [widget, vps, notes], attention: [{ sessionId: "flagged" }] }, inboxMode: "current", showArchived: false, expandedProjects: new Set(), openArchivedProjects: false, selected: null },
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
assert.match(html, /<span class="host-badge" title="On vps\.example\.ts\.net">vps<\/span>/, "a remote project names its host briefly");
assert.match(html, /data-session="far".*<span class="host-badge" title="On vps\.example\.ts\.net">vps<\/span>/s, "a remote session shows a short host badge");
assert.doesNotMatch(html.slice(0, html.indexOf("</section>")), /host-badge/, "local projects and sessions carry no host badge");
assert.doesNotMatch(html, /data-session="hidden"|data-session="loose"/, "archived sessions and sessions without a project stay out");
assert.match(html, /Archived \(1\)<\/summary>.*data-project-action="project-unarchive" data-project-id="project-notes".*data-session="kept"/s);
assert.match(widgetSection, /data-project-message="project-widget" data-project-name="widget"[^>]*>Message<\/button>/, "an active project offers a message to its conversations");
assert.doesNotMatch(html.slice(html.indexOf("Archived (1)")), /data-project-message/, "an archived project offers no message");
const local = { id: "h0", name: "laptop", state: "local" };
const remote = { id: "h1", name: "vps.example.ts.net", state: "connected" };
const leads = (coordinators) => {
  context.app.state.projects = [{ ...widget, coordinators }];
  const rendered = vm.runInContext('renderProjects("")', context).html;
  return rendered.slice(0, rendered.indexOf("</section>"));
};
assert.match(leads([{ host: local, name: "widget-lead", surface: "claude", sessionId: "lead" }]), /<button class="lead-chip" type="button" data-session="lead" title="Open @widget-lead, the project coordinator">@widget-lead<\/button>/, "a started coordinator opens its conversation");
assert.match(leads([{ host: local, name: "widget-lead", surface: "claude" }]), /<span class="lead-chip idle" title="@widget-lead starts on its first message">@widget-lead<\/span>/, "an unstarted coordinator is not clickable");
assert.match(leads([{ host: remote, name: "vps-lead", surface: "codex", sessionId: "h1::s9" }]), /data-session="h1::s9"[^>]*>vps::@vps-lead</, "a paired host's coordinator is named with its host and opens its mirrored session");
const conflict = leads([{ host: local, name: "a<b", surface: "claude", sessionId: "lead" }, { host: remote, name: "vps-lead", surface: "codex" }]);
assert.match(conflict, /<span class="lead-chip conflict" title="Several hosts each have a coordinator: @a&lt;b, vps::@vps-lead\.[^"]*">2 leads<\/span>/, "a conflict names every coordinator and opens none");
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

const metaStart = source.indexOf("function conversationMeta(");
const metaEnd = source.indexOf("function surfaceIcon(", metaStart);
assert(metaStart >= 0 && metaEnd > metaStart);
const metaContext = { backgroundWaiting: () => false, statusLabel: (status) => status, timeAgo: () => "now" };
vm.runInNewContext(source.slice(metaStart, metaEnd), metaContext);
const meta = (configRoot) => vm.runInContext(`conversationMeta(${JSON.stringify({ status: "busy", queueCount: 0, claudeConfigRoot: configRoot })})`, metaContext);
assert(meta({ name: "work", root: "/w", signedInAs: "work@example.com" }).includes("config folder work · currently signed in as work@example.com"), "a session's config folder shows its current login");
assert(meta({ name: "default", root: "/d", signedInAs: "me@example.com" }).includes("config folder default · currently signed in as me@example.com"), "the primary folder shows its login too");
assert(meta({ name: "spare", root: "/s" }).endsWith("config folder spare"), "a folder with no login shows its name");
assert(!meta({ name: "default", root: "/d" }).includes("config folder"), "the primary folder with no login adds nothing");
