import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";
import vm from "node:vm";

const widget = { id: "project-widget", displayName: "widget", rootPath: "/work/widget", kind: "git" };
const sessions = Array.from({length:9}, (_, index) => ({id:`w${index}`, name:`w${index}`, surface:"codex", status:"idle", display:{kind:"idle",label:`Sent ${index}`,detail:""}}));
const group = {id:widget.id, name:widget.displayName, counts:{families:9,sessions:9}, collapsedFamilyIds:["w8","w6","w0","w2","w3","w4","w5"]};
const context = {
  app: { state: { sessions, projects: [widget] }, sessionView: {sessions, view:{projects:[group], families:["w8","w6","w0","w2","w3","w4","w5","w1","w7"].map(id=>({id,projectId:widget.id,members:[{id}]}))}}, expandedProjects:new Set(), selected:null },
};
await dashboardModules(["projects", "holders", "session-row", "project-model", "hosts"], context);
const {html} = context.renderProjects();
assert.match(html, /title="9 conversations">9<\/span>/);
const widgetSection = html.slice(0,html.indexOf("</section>"));
assert.deepEqual([...widgetSection.matchAll(/data-session="([^"]+)"/g)].map(m=>m[1]), group.collapsedFamilyIds);
assert.match(widgetSection, /Show 2 more/);
context.app.expandedProjects.add(widget.id);
assert.equal([...context.renderProjects().html.matchAll(/data-session=/g)].length,9);
context.app.expandedProjects.clear();
assert.doesNotMatch(html, /\/work\/widget/);
assert.match(widgetSection, /data-project-message="project-widget"/);
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

const folder = { value: "~" };
const select = { innerHTML: "", value: "" };
const picker = {
  app: { startOptions: { projects: [{ id: "p-api", displayName: "api", rootPath: "/work/api", kind: "git" }, { id: "p-web", displayName: "web <x>", rootPath: "/work/web", kind: "git" }] } },
  escape: (value) => String(value).replaceAll("<", "&lt;").replaceAll(">", "&gt;"),
  $: (selector) => (selector === "#new-conversation-project" ? select : { querySelector: () => folder }),
};
await dashboardModules(["start-form"], picker);
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

const metaContext = { backgroundWaiting: () => false, statusLabel: (status) => status, workingLabel: () => "Working", timeAgo: () => "now" };
await dashboardModules(["session-labels"], metaContext);
const meta = (configRoot) => vm.runInContext(`conversationMeta(${JSON.stringify({ display: {kind:"working",label:"Working",detail:""}, status: "busy", queueCount: 0, claudeConfigRoot: configRoot })})`, metaContext);
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
context.app.sessionView.view.projects = [{id:shared.id, counts:{families:0,sessions:0},collapsedFamilyIds:[]}];
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
await dashboardModules(["actions"], actions);
reply = { ok: false, status: 409, text: async () => '{"error":{"code":"coordinator_exists","message":"the project already has coordinator @widget-lead; replace it to take over"}}' };
await assert.rejects(vm.runInContext('action("coordinator-new", { surface: "claude", cwd: "/work/widget" }, "key", "")', actions), (error) => error.code === "coordinator_exists" && error.message === "the project already has coordinator @widget-lead; replace it to take over", "a typed refusal keeps its code and shows its message");
reply = { ok: false, status: 400, text: async () => "plain refusal" };
await assert.rejects(vm.runInContext('action("project-rename", { projectId: "p", title: "x" }, "key", "")', actions), (error) => !error.code && error.message === "plain refusal");
assert.equal(sent.length, 2, "project and lead actions run with no conversation selected");

const asked = [];
let answer = true;
const leadContext = { window: { confirm: (question) => { asked.push(question); return answer; } } };
await dashboardModules(["project-dialog"], leadContext);
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
