import "./test-home.mjs";
import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import vm from "node:vm";

const now = Date.UTC(2026, 10, 3, 12);
const at = (minutes) => new Date(now - minutes * 60e3).toISOString();
const project = { id: "repo:github.com/demo/fieldnotes", displayName: "fieldnotes", rootPath: "/work/fieldnotes", kind: "git" };
const buildbox = { id: "buildbox", name: "buildbox.example.net", state: "connected" };
const sessions = [
  { id: "lead", name: "release-lead", surface: "codex", status: "idle", lastActive: at(3 * 24 * 60), project, current: true, capabilities: { lifecycle: { resume: true } } },
  { id: "build", name: "build", surface: "claude", status: "busy", lastActive: at(1), project, current: true },
  { id: "queued", name: "queued", surface: "claude", status: "idle", lastActive: at(9 * 60), project, current: true, queueCount: 1 },
  { id: "photos", name: "photos", surface: "claude", status: "idle", lastActive: at(2 * 60), project, current: true },
  { id: "buildbox::ingest", name: "ingest", surface: "claude", status: "idle", lastActive: at(40), project, current: true, host: buildbox },
  { id: "old", name: "old", surface: "claude", status: "idle", lastActive: at(6 * 24 * 60), project },
  { id: "older", name: "older", surface: "claude", status: "idle", lastActive: at(9 * 24 * 60), project },
  { id: "child", name: "child", surface: "claude", status: "busy", lastActive: at(1), project, current: true, subagent: { parentId: "build" } },
  { id: "shelved", name: "shelved", surface: "claude", status: "idle", lastActive: at(5), project, archived: true },
  { id: "elsewhere", name: "elsewhere", surface: "claude", status: "busy", lastActive: at(1), project: { ...project, id: "repo:github.com/demo/other" }, current: true },
];
const projects = [{
  ...project,
  checkouts: [
    { host: { id: "studio", name: "studio-mac", state: "local" }, projectId: "implicit:/work/fieldnotes", rootPath: "/work/fieldnotes", added: true },
    { host: buildbox, projectId: "buildbox::implicit:/srv/fieldnotes", rootPath: "/srv/fieldnotes" },
  ],
  coordinators: [{ name: "release-lead", surface: "codex", sessionId: "lead" }],
}];
const family = (id, ...children) => ({ id, rootId: id, projectId: project.id, members: [{ id, depth: 0, contextOnly: false }, ...children.map((child) => ({ id: child, depth: 1, contextOnly: false }))] });
const projectListing = {
  view: { projects: [{ id: project.id, collapsedFamilyIds: [] }] },
  families: [family("queued"), family("build", "child"), family("photos"), family("buildbox::ingest"), family("lead"), family("old"), family("older"), { ...family("elsewhere"), projectId: "repo:github.com/demo/other" }],
  rows: new Map(sessions.map((session) => [session.id, session])),
  cursor: "",
};
const context = {
  app: { state: { sessions, projects, attention: [] }, sessionOrder: {}, selected: null, selectedProject: null, projectEarlier: null },
  labels: { codex: "Codex", claude: "Claude Code" },
  Date: class extends Date { static now() { return now; } },
};
await dashboardModules(["session-order", "project-view", "project-tiles", "project-now", "session-row", "holders", "presence", "hosts", "session-labels", "format"], context);
const run = (code) => vm.runInContext(code, context);
let html = run("renderProjectView(app.state.projects[0])");
assert.match(html, /Loading this project/, "a project page waits for its own listing rather than showing an empty or stale one");
context.app.sessionOrder[`project:all:${project.id}`] = projectListing;
const ids = (list) => list.map((session) => session.id).join(",");

const contents = run("projectContents(app.state.projects[0])");
assert.equal(contents.leadSession.id, "lead");
const nowIDs = (now) => ids(now.groups.flatMap((group) => group.sessions));
assert.equal(nowIDs(contents.now), "build,photos,queued,buildbox::ingest", "Now is every running and current row, working first, then newest, each computer's rows together, and leaves out the lead");
assert.equal(run("sessionRunning")(sessions[2]), false, "a queued idle session isn't running");
assert.equal(ids(contents.earlier), "old,older");
for (const hidden of ["child", "shelved", "elsewhere"]) assert(!JSON.stringify(contents).includes(`"${hidden}"`), `${hidden} does not belong in the project`);

const actions = (session) => JSON.stringify(run("leadActions")(session));
assert.equal(actions(sessions[1]), JSON.stringify([{ kind: "open" }, { kind: "message" }]), "a working lead offers Open and Message");
assert.equal(actions(sessions[0]), JSON.stringify([{ kind: "resume", enabled: true, lifecycle: true }, { kind: "open" }]));
assert.equal(actions({ ...sessions[0], capabilities: {} }), JSON.stringify([{ kind: "resume", enabled: true, lifecycle: false }, { kind: "open" }]), "Resume without the lifecycle capability opens the lead to message it");
assert.equal(actions({ ...sessions[0], unavailableReason: "buildbox can't be reached" }), JSON.stringify([{ kind: "resume", enabled: false, lifecycle: false }, { kind: "open" }]), "an offline lead's Resume is disabled");
assert.equal(run('leadSessionID({ name: "far", sessionId: "s1", host: { id: "buildbox", state: "connected" } })'), "buildbox::s1", "a remote lead's session is named by its computer");
assert.equal(run('leadSessionID({ name: "near", sessionId: "s1", host: { id: "studio", state: "local" } })'), "s1");

assert.equal(run('leadNote({ name: "fresh" })'), "starts with the first message sent to it");
assert.equal(run('leadNote({ name: "ended", sessionId: "s1" })'), "not running · the next message picks it up", "a lead with a conversation never claims it has not started");

html = run("renderProjectView(app.state.projects[0])");
assert.match(html, /<h1>fieldnotes<\/h1><span class="project-note">7 sessions on 2 computers<\/span>/);
assert.match(html, /<h2>Now · 4<\/h2>/);
assert.doesNotMatch(html, /Running now|Recent/, "Now replaces both cards");
assert.match(html, /<h3 class="project-now-group">No folder · this computer<\/h3>/, "rows on two computers are separate groups, named by computer");
assert.match(html, /<h3 class="project-now-group">No folder · buildbox<\/h3>/);
assert.match(html, /data-project-lead-status>Codex · Idle · 3 days ago</, "the lead says when it was last active, once");
assert.match(html, /<button class="primary-button" type="button" data-project-resume="lead" data-lifecycle="true">Resume<\/button>/);
assert.match(html, /data-project-start="repo:github.com\/demo\/fieldnotes" data-checkout="0">New session/);
assert.match(html, /data-project-earlier=/);
assert.doesNotMatch(html, /data-session="old"/, "Earlier stays closed until asked");
assert.doesNotMatch(html, /data-project-lead-replaced/, "a lead never replaced says nothing about it");
assert.match(html, /<button class="soft-button" type="button" data-project-brief="repo:github.com\/demo\/fieldnotes">Catch me up<\/button>/, "a project with a lead offers to catch the person up");
assert.equal(run('briefToast({ ref: "@release-lead", since: { at: "2026-11-03T09:00:00Z", fallback: true } })'), "Asked @release-lead to catch you up on the last 24 hours.", "with nothing written to the lead, the brief covers a day");
assert.match(run('briefToast({ ref: "@release-lead", since: { at: "2026-11-03T09:00:00Z", fallback: false } })'), /^Asked @release-lead to catch you up since /, "otherwise it names when the person last wrote");
assert.equal(run('briefToast({ ref: "@release-lead" })'), "Asked @release-lead to catch you up.", "a brief held for an unreachable computer has no window yet");
context.app.projectEarlier = project.id;
html = run("renderProjectView(app.state.projects[0])");
assert.match(html, /data-session="old"/);
projects[0].coordinators[0].replaced = { id: 3, name: "release-lead", sessionId: "lead", previousSessionId: "gone", changedBy: "the daemon", reason: "conversation gone no longer exists", at: at(5) };
html = run("renderProjectView(app.state.projects[0])");
assert.match(html, /data-project-lead-replaced>Replaced [^<]+ by the daemon: conversation gone no longer exists</, "the lead card says when, by whom and why it was replaced");
delete projects[0].coordinators[0].replaced;

assert.equal(run("rowTime")({ ...sessions[1], activity: { busySince: at(25) } }, now), "25m", "a running row shows how long its turn has run");
assert.equal(run("rowTime")(sessions[1], now), "now", "a running row without a start shows now, never its last activity");
assert.equal(run("rowTime")(sessions[3], now), "2h", "an idle row shows its last activity");
assert.match(html, /<span class="project-row-time">2h<\/span>/);

const waiting = { ...sessions[1], id: "asking", name: "asking", waitingOn: { kind: "question", id: "q", answerable: true }, lastActive: at(4), activity: { busySince: at(90) } };
const unreachable = { ...sessions[1], id: "buildbox::far", name: "far", host: { ...buildbox, state: "unreachable" }, lastActive: at(7), activity: { busySince: at(90) } };
const sessionRow = run("sessionButton");
assert.match(sessionRow(waiting), /<i class="dot waiting">/, "waiting on you has its own colour");
assert.match(sessionRow(waiting), /Waiting on you/);
assert.match(sessionRow(unreachable), /<i class="dot unavailable">/);
assert.match(sessionRow(unreachable), /Unavailable/);
assert.doesNotMatch(sessionRow(unreachable), /Working/, "a busy row on an unreachable computer doesn't claim to work");
assert.match(sessionRow(sessions[3]), /<i class="dot idle">/);
assert.match(sessionRow({ ...sessions[3], currentReason: "open" }), /<i class="dot idle">/, "an open idle session isn't marked as working");
assert.match(sessionRow(sessions[1]), /<i class="dot active">/);
assert.match(sessionRow({ ...sessions[3], currentReason: "queued" }), /<i class="dot queued">/);
assert.equal(run("rowTime")(waiting, now), "4m", "a waiting row shows its last activity, as Mac and iPhone do");
assert.equal(run("rowTime")(unreachable, now), "7m", "an unreachable row shows its last activity");
const tokens = readFileSync(new URL("../internal/daemon/dashboard/tokens.css", import.meta.url), "utf8");
const dotColour = (tone) => tokens.match(new RegExp(`\\.dot\\.${tone}[^{]*\\{\\s*background: var\\(--([a-z]+)\\)`))?.[1];
assert.equal(dotColour("waiting"), "warn");
assert.equal(dotColour("idle"), "quiet");
assert.equal(dotColour("unavailable"), "bad");
assert.equal(dotColour("active"), "good");

const workingLead = { ...sessions[0], status: "busy", activity: { busySince: at(12) } };
context.app.state.sessions = [workingLead, ...sessions.slice(1)];
const pinned = run("projectContents(app.state.projects[0], app.state.sessions)");
assert.equal(pinned.leadSession.id, "lead");
assert(!nowIDs(pinned.now).split(",").includes("lead"), "a working lead stays pinned and is not repeated in Now");
html = run("renderProjectView(app.state.projects[0])");
assert.match(html, /data-project-lead-status>Codex · Working 12m</);
const blockedLead = (patch) => {
  context.app.state.sessions = [{ ...workingLead, lastActive: at(30), ...patch }, ...sessions.slice(1)];
  return run("renderProjectView(app.state.projects[0])");
};
assert.match(blockedLead({ waitingOn: { kind: "question", id: "q", answerable: true } }), /data-project-lead-status>Codex · Waiting on you · 30 min ago</, "a lead waiting on you says so, with its last activity");
assert.match(blockedLead({ host: { ...buildbox, state: "unreachable" }, unavailableReason: "buildbox can't be reached" }), /data-project-lead-status>Codex · [^<]*Unavailable · 30 min ago</, "a lead on an unreachable computer doesn't claim to work");
const delegating = { ...sessions[0], subagents: { count: 1, working: 1 } };
context.app.state.sessions = [delegating, ...sessions.slice(1)];
const delegated = run("projectContents(app.state.projects[0], app.state.sessions)");
assert(!nowIDs(delegated.now).split(",").includes("lead"), "a lead whose subagent works stays pinned, not in Now");
assert.equal(actions(delegating), JSON.stringify([{ kind: "resume", enabled: true, lifecycle: true }, { kind: "open" }]), "an idle lead with a working subagent can still be resumed");
html = run("renderProjectView(app.state.projects[0])");
assert.match(html, /data-project-lead-status>Codex · Idle · 3 days ago</, "an idle lead with a working subagent reads as idle");
for (const lastActive of ["0001-01-01T00:00:00Z", "", undefined]) {
  const unknown = { ...sessions[3], lastActive };
  assert.equal(run("rowTime")(unknown, now), "", "a row with no known activity shows no age");
  assert.equal(run("idleFor")(unknown, now), "", "a lead with no known activity shows no age");
}
context.app.state.sessions = [{ ...sessions[0], lastActive: "0001-01-01T00:00:00Z" }, ...sessions.slice(1)];
html = run("renderProjectView(app.state.projects[0])");
assert.match(html, /data-project-lead-status>Codex · Idle</, "a lead with no known activity reads as idle alone");
context.app.state.sessions = sessions;

context.app.state.projects = [{ ...projects[0], coordinators: [] }];
html = run("renderProjectView(app.state.projects[0])");
assert.match(html, /No lead yet/);
assert.doesNotMatch(html, /data-project-brief/, "a project without a lead has nobody to catch the person up");
assert.match(html, /data-project-lead="repo:github.com\/demo\/fieldnotes">Start a lead</);

const panel = { classList: { hidden: false, contains: () => panel.classList.hidden } };
const pane = { hidden: true, innerHTML: "" };
let renders = 0;
const paneContext = {
  app: { state: { projects }, selectedProject: project.id },
  document: { querySelector: (selector) => selector === '[data-view-panel="conversations"]' ? panel : { hidden: false } },
  $: () => pane,
  renderProjectView: () => `rendered ${++renders}`,
  showView() {}, syncConversationLayout() {}, mobileConversationView: () => false,
  renderOverview() {}, renderSurfaceFilter() {}, renderStartSurfaceOptions() {}, renderSessions() {}, renderOperations() {},
};
await dashboardModules(["project-pane", "dashboard-state"], paneContext);
panel.classList.hidden = true;
vm.runInContext("renderAll", paneContext)();
assert.equal(renders, 0, "the 30 s state refresh does not rebuild a project hidden behind another view");
panel.classList.hidden = false;
const refresh = vm.runInContext("refreshShownProject", paneContext);
refresh();
assert.equal(pane.innerHTML, "rendered 1", "the shown project refreshes while Conversations is open");
panel.classList.hidden = true;
refresh();
assert.equal(renders, 1, "a project left behind another view is not rebuilt each minute");
panel.classList.hidden = false;
refresh();
assert.equal(pane.innerHTML, "rendered 2", "returning to Conversations resumes the refresh");
console.log("dashboard project view tests passed");
