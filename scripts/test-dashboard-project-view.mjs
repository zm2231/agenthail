import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";
import vm from "node:vm";

const now = Date.UTC(2026, 10, 3, 12);
const at = (minutes) => new Date(now - minutes * 60e3).toISOString();
const project = { id: "repo:github.com/demo/fieldnotes", displayName: "fieldnotes", rootPath: "/work/fieldnotes", kind: "git" };
const forge = { id: "forge", name: "forge-agents.example.ts.net", state: "connected" };
const sessions = [
  { id: "lead", name: "release-lead", surface: "codex", status: "idle", lastActive: at(3 * 24 * 60), project, current: true, capabilities: { lifecycle: { resume: true } } },
  { id: "build", name: "build", surface: "claude", status: "busy", lastActive: at(1), project, current: true },
  { id: "queued", name: "queued", surface: "claude", status: "idle", lastActive: at(9 * 60), project, current: true, queueCount: 1 },
  { id: "photos", name: "photos", surface: "claude", status: "idle", lastActive: at(2 * 60), project, current: true },
  { id: "forge::ingest", name: "ingest", surface: "claude", status: "idle", lastActive: at(40), project, current: true, host: forge },
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
    { host: forge, projectId: "forge::implicit:/srv/fieldnotes", rootPath: "/srv/fieldnotes" },
  ],
  coordinators: [{ name: "release-lead", surface: "codex", sessionId: "lead" }],
}];
const context = {
  app: { state: { sessions, projects, attention: [] }, selected: null, selectedProject: null, projectEarlier: null },
  labels: { codex: "Codex", claude: "Claude Code" },
  Date: class extends Date { static now() { return now; } },
};
await dashboardModules(["project-view", "session-row", "holders", "presence", "hosts", "session-labels", "format"], context);
const run = (code) => vm.runInContext(code, context);
const ids = (list) => list.map((session) => session.id).join(",");

const contents = run("projectContents(app.state.projects[0], app.state.sessions)");
assert.equal(contents.leadSession.id, "lead");
assert.equal(ids(contents.running), "build", "only working sessions are running; a queued idle one is not");
assert.equal(ids(contents.running), ids(sessions.filter((session) => session.project.id === project.id && !session.subagent && run("sessionRunning")(session))), "Running agrees with the list's running rule");
assert.equal(ids(contents.recent), "forge::ingest,photos,queued", "Recent is newest first across computers and leaves out the lead");
assert.equal(ids(contents.earlier), "old,older");
for (const hidden of ["child", "shelved", "elsewhere"]) assert(!JSON.stringify(contents).includes(`"${hidden}"`), `${hidden} does not belong in the project`);

const actions = (session) => JSON.stringify(run("leadActions")(session));
assert.equal(actions(sessions[1]), JSON.stringify([{ kind: "open" }, { kind: "message" }]), "a working lead offers Open and Message");
assert.equal(actions(sessions[0]), JSON.stringify([{ kind: "resume", enabled: true, lifecycle: true }, { kind: "open" }]));
assert.equal(actions({ ...sessions[0], capabilities: {} }), JSON.stringify([{ kind: "resume", enabled: true, lifecycle: false }, { kind: "open" }]), "Resume without the lifecycle capability opens the lead to message it");
assert.equal(actions({ ...sessions[0], unavailableReason: "forge can't be reached" }), JSON.stringify([{ kind: "resume", enabled: false, lifecycle: false }, { kind: "open" }]), "an offline lead's Resume is disabled");
assert.equal(run('leadSessionID({ name: "far", sessionId: "s1", host: { id: "forge", state: "connected" } })'), "forge::s1", "a remote lead's session is named by its computer");
assert.equal(run('leadSessionID({ name: "near", sessionId: "s1", host: { id: "studio", state: "local" } })'), "s1");

let html = run("renderProjectView(app.state.projects[0])");
assert.match(html, /<h1>fieldnotes<\/h1><span class="project-note">7 sessions on 2 computers<\/span>/);
assert.match(html, /Running now · 1/);
assert.match(html, /data-project-lead-status>Codex · Idle · 3 days ago</, "the lead says when it was last active, once");
assert.match(html, /<button class="primary-button" type="button" data-project-resume="lead" data-lifecycle="true">Resume<\/button>/);
assert.match(html, /data-project-start="repo:github.com\/demo\/fieldnotes" data-checkout="0">New session/);
assert.match(html, /data-project-earlier=/);
assert.doesNotMatch(html, /data-session="old"/, "Earlier stays closed until asked");
context.app.projectEarlier = project.id;
html = run("renderProjectView(app.state.projects[0])");
assert.match(html, /data-session="old"/);

assert.equal(run("rowTime")({ ...sessions[1], activity: { busySince: at(25) } }, now), "25m", "a running row shows how long its turn has run");
assert.equal(run("rowTime")(sessions[1], now), "now", "a running row without a start shows now, never its last activity");
assert.equal(run("rowTime")(sessions[3], now), "2h", "an idle row shows its last activity");
assert.match(html, /<span class="project-row-time">2h<\/span>/);

const workingLead = { ...sessions[0], status: "busy", activity: { busySince: at(12) } };
context.app.state.sessions = [workingLead, ...sessions.slice(1)];
const pinned = run("projectContents(app.state.projects[0], app.state.sessions)");
assert.equal(pinned.leadSession.id, "lead");
assert.equal(ids(pinned.running), "build", "a working lead stays pinned and is not repeated in Running");
html = run("renderProjectView(app.state.projects[0])");
assert.match(html, /data-project-lead-status>Codex · Working 12m</);
const delegating = { ...sessions[0], subagents: { count: 1, working: 1 } };
context.app.state.sessions = [delegating, ...sessions.slice(1)];
const delegated = run("projectContents(app.state.projects[0], app.state.sessions)");
assert.equal(ids(delegated.running), "build", "a lead whose subagent works stays pinned, not in Running");
assert.equal(actions(delegating), JSON.stringify([{ kind: "resume", enabled: true, lifecycle: true }, { kind: "open" }]), "an idle lead with a working subagent can still be resumed");
html = run("renderProjectView(app.state.projects[0])");
assert.match(html, /data-project-lead-status>Codex · Idle · 3 days ago</, "an idle lead with a working subagent reads as idle");
context.app.state.sessions = sessions;

context.app.state.projects = [{ ...projects[0], coordinators: [] }];
html = run("renderProjectView(app.state.projects[0])");
assert.match(html, /No lead yet/);
assert.match(html, /data-project-lead="repo:github.com\/demo\/fieldnotes">Start a lead</);
console.log("dashboard project view tests passed");
