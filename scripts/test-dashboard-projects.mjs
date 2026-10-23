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

const laptop = { id: "h0", name: "laptop", state: "local" };
const server = { id: "h1", name: "server.example.ts.net", state: "connected" };
const menu = (project) => vm.runInContext(`projectMenu(${JSON.stringify(project)})`, context);
const shared = { id: "repo:github.com/example/widget", displayName: "widget", checkouts: [
  { host: laptop, projectId: "p-laptop", rootPath: "/work/widget", added: true },
  { host: server, projectId: "h1::implicit:/srv/widget", rootPath: "/srv/widget" },
], coordinators: [{ host: laptop, name: "widget-lead", surface: "claude", sessionId: "lead" }] };
const sharedMenu = menu(shared);
context.app.state.projects = [shared];
const sharedSection = vm.runInContext('renderProjects("")', context).html;
assert.match(sharedSection, /<span class="host-badge" title="On server\.example\.ts\.net">server<\/span>/, "a remote checkout's badge names its host");
assert.doesNotMatch(sharedSection, /\/srv\/widget|\/work\/widget/, "the projects list never shows a checkout's folder, not even in an attribute");
assert.match(sharedMenu, /data-project-rename="repo:github\.com\/example\/widget">Rename…</, "a project added on some host can be renamed");
assert.match(sharedMenu, /data-project-start="repo:github\.com\/example\/widget" data-checkout="0">New conversation on laptop<.*data-checkout="1">New conversation on server</s, "each checkout offers a new conversation, named by its host");
assert.match(sharedMenu, />Change lead…<.*data-project-clear-lead="repo:github\.com\/example\/widget">Clear lead</s, "a project with a lead can change or clear it");
assert.match(sharedMenu, /data-project-action="project-remove" data-project-id="p-laptop">Remove on laptop</, "an added checkout can be removed on its host");
assert.doesNotMatch(sharedMenu, /h1::implicit|\/srv\/widget|\/work\/widget/, "the menu never shows a folder and offers no remove for an implicit checkout");
const implicitMenu = menu({ id: "h1::implicit:/srv/api", displayName: "api", checkouts: [{ host: server, projectId: "h1::implicit:/srv/api", rootPath: "/srv/api" }], coordinators: [{ host: laptop, name: "a" }, { host: server, name: "b" }] });
assert.doesNotMatch(implicitMenu, /Rename|Remove|Clear lead/, "a project added nowhere has no name of its own, nothing to remove, and a conflict is changed rather than cleared");
assert.match(implicitMenu, /New conversation on server<.*>Change lead…</s, "a project only on a paired host names that host");
assert.match(menu({ id: "p-notes", displayName: "notes", checkouts: [{ host: laptop, projectId: "p-notes", rootPath: "/work/notes", added: true }] }), /data-checkout="0">New conversation<.*>Set lead…</s, "a project only on this machine names no host");
const archivedMenu = menu({ id: "p-old", displayName: "old", archived: true, checkouts: [{ host: laptop, projectId: "p-old", rootPath: "/work/old", added: true, archived: true }] });
assert.doesNotMatch(archivedMenu, /New conversation|lead/, "an archived project starts nothing and changes no lead");
assert.match(archivedMenu, />Rename…<.*>Remove</s);
assert.match(vm.runInContext('renderProjects("")', context).html, /^<div class="projects-toolbar"><button[^>]*data-project-new>Add project…<\/button><\/div>/, "the projects view opens with Add project");

const suggestions = vm.runInContext(`projectFolderSuggestions(${JSON.stringify({ projects: [{ rootPath: "/srv/api", added: false }, { rootPath: "/srv/notes", added: true }], folders: ["/srv/scratch", "/srv/api"] })})`, context);
assert.deepEqual([...suggestions], ["/srv/api", "/srv/scratch"], "suggestions are repositories not yet added, then session folders, each once");
assert.deepEqual([...vm.runInContext("projectFolderSuggestions(null)", context)], []);
const lead = (checkout, values, replace) => vm.runInContext(`leadRequest(${JSON.stringify(checkout)}, ${JSON.stringify(values)}, ${replace})`, context);
assert.deepEqual({ ...lead(shared.checkouts[1], { surface: "codex", name: "  " }, false) }, { surface: "codex", cwd: "/srv/widget", host: "h1" }, "a lead on a paired host names that host and leaves the name to it");
assert.deepEqual({ ...lead(shared.checkouts[0], { surface: "claude", name: " widget-lead " }, true) }, { surface: "claude", cwd: "/work/widget", name: "widget-lead", replace: true }, "a lead on this machine names no host");

const actionStart = source.indexOf("async function action(");
const actionEnd = source.indexOf("function deliveryStatusLabel(", actionStart);
assert(actionStart >= 0 && actionEnd > actionStart);
const sent = [];
let reply;
const actions = {
  app: { selected: null },
  crypto: { randomUUID: () => "key" },
  withHolder: () => assert.fail("a project or lead action carries no holder"),
  fetch: async (_url, request) => {
    sent.push(JSON.parse(request.body));
    return reply;
  },
};
vm.runInNewContext(source.slice(actionStart, actionEnd), actions);
reply = { ok: false, status: 409, text: async () => '{"error":{"code":"coordinator_exists","message":"the project already has coordinator @widget-lead; replace it to take over"}}' };
await assert.rejects(vm.runInContext('action("coordinator-new", { surface: "claude", cwd: "/work/widget" }, "key", "")', actions), (error) => error.code === "coordinator_exists" && error.message === "the project already has coordinator @widget-lead; replace it to take over", "a typed refusal keeps its code and shows its message");
reply = { ok: false, status: 400, text: async () => "plain refusal" };
await assert.rejects(vm.runInContext('action("project-rename", { projectId: "p", title: "x" }, "key", "")', actions), (error) => !error.code && error.message === "plain refusal");
assert.equal(sent.length, 2, "project and lead actions run with no conversation selected");

const leadStart = source.indexOf("async function changeLead(");
const leadEnd = source.indexOf("const projectDialogFields", leadStart);
assert(leadStart >= 0 && leadEnd > leadStart);
const asked = [];
let answer = true;
const leadContext = { window: { confirm: (question) => { asked.push(question); return answer; } } };
vm.runInNewContext(source.slice(leadStart, leadEnd), leadContext);
const refusal = Object.assign(Error("the project already has coordinator @widget-lead; replace it to take over"), { code: "coordinator_exists" });
const attempts = [];
const send = async (replace) => { attempts.push(replace); if (!replace) throw refusal; return { coordinator: { name: "new-lead" } }; };
assert.equal((await leadContext.changeLead(send)).coordinator.name, "new-lead");
assert.deepEqual(attempts, [false, true], "a refused lead change is sent again as a replacement once the person agrees");
assert.match(asked[0], /@widget-lead; replace it to take over\. Replace it\?$/);
answer = false;
attempts.length = 0;
assert.equal(await leadContext.changeLead(send), null, "declining keeps the current lead");
assert.deepEqual(attempts, [false]);
await assert.rejects(leadContext.changeLead(async () => { throw Object.assign(Error("name taken"), { code: "name_taken" }); }), /name taken/, "other refusals are not offered as a replacement");
console.log("dashboard project management tests passed");
