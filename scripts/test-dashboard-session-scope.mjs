import "./test-home.mjs";
import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";
import vm from "node:vm";

const elements = {};
const element = (id) => (elements[id] ||= { innerHTML: "", textContent: "", hidden: false, value: "", classList: { add() {}, remove() {}, toggle() {} } });
const sessions = [
  { id: "p1", name: "p-one", surface: "claude", project: { id: "P" } },
  { id: "p2", name: "p-two", surface: "codex", project: { id: "P" } },
  { id: "q1", name: "q-one", surface: "claude", project: { id: "Q" } },
];
const app = {
  state: { counts: {}, projects: [{ id: "P" }, { id: "Q" }] },
  inboxMode: "projects",
  scope: "all",
  sessionLimit: 80,
  sessionViewport: false,
  codexSearch: { query: "", results: [{ id: "h1", name: "old-p", project: { id: "P" } }, { id: "h2", name: "old-q", project: { id: "Q" } }] },
  filters: {},
};
const context = vm.createContext({
  app,
  window: { matchMedia: () => ({ matches: false }) },
  document: { querySelectorAll: () => [] },
  $: (selector) => element(selector),
  escape: (value) => String(value),
  labels: {},
  displayName: (session) => session.name,
  rawDisplayName: (session) => session.name,
  remoteHostName: () => "",
  sessionButton: (session) => `[${session.id}]`,
  scopeOrderKeys: () => (["all", "inbox"].includes(app.scope) ? [] : [`scope:all:${app.scope}`]),
  scopedSessions: () => (!["all", "inbox"].includes(app.scope) ? sessions.filter((session) => session.project?.id === app.scope) : app.inboxMode === "projects" ? sessions.filter((session) => session.project) : sessions),
  orderReady: (keys) => !keys.includes("scope:all:loading"),
  listOrderScopes: () => ["all"],
  listedTotal: () => 40,
  listingHasMore: () => true,
  renderProjects: () => ({ count: 2, html: "[project directory]" }),
  renderInboxScope: () => { element("#session-list").innerHTML = "[inbox]"; },
});
await dashboardModules(["sessions"], context);
const render = () => { vm.runInContext("renderSessions()", context); return element("#session-list").innerHTML; };

assert.equal(render(), "[project directory]", "Projects mode without a project scope lists the projects");
app.scope = "P";
const scopedProjects = render();
assert.ok(scopedProjects.startsWith("[p1][p2]") && !scopedProjects.includes("project directory") && !scopedProjects.includes("q1"), "a project tile scopes Projects mode to that project's sessions");
assert.ok(scopedProjects.includes('data-session="h1"') && !scopedProjects.includes('data-session="h2"'), "and its Codex history matches");
app.inboxMode = "all";
const scopedAll = render();
assert.ok(scopedAll.startsWith("[p1][p2]") && !scopedAll.includes("q1"), "a project scope limits the paged list");
assert.ok(scopedAll.includes('data-session="h1"') && !scopedAll.includes('data-session="h2"'), "Codex history matches are limited to the project");
assert.equal(element("#conversation-count").textContent, "2", "the count is the project's, not the whole catalog's");
assert.equal(element("#session-more").textContent, "Show more", "Show more does not claim a whole-catalog count");
assert.ok(element("#filter-summary").textContent.includes("in this project"), "the summary says the list is the project's");
app.inboxMode = "projects";
render();
assert.equal(element("#session-filters").hidden, false, "a project scope keeps the surface and status filters");
app.scope = "loading";
assert.ok(render().includes("Loading"), "a project scope waits for its own listing");
app.inboxMode = "all";
app.scope = "inbox";
assert.equal(render(), "[inbox]", "the Inbox scope shows the decision groups");
app.scope = "all";
assert.ok(render().includes("[q1]"), "All shows every session again");
const committed = [];
const tiles = vm.createContext({
  app: { scope: "all", state: { projects: [{ id: "P" }, { id: "Q", archived: true }] }, selectedProject: null },
  showView: () => committed.push(tiles.app.scope),
  openProject: (id) => { tiles.showView(); tiles.app.selectedProject = id; },
  leaveProject: () => { tiles.app.selectedProject = null; },
  syncDecisionPane: () => {},
  renderSessions: () => {},
  shownDecisions: () => [],
  $: () => null,
});
await dashboardModules(["project-tiles-view"], tiles);
vm.runInContext('setScope("P")', tiles);
vm.runInContext('setScope("inbox")', tiles);
assert.deepEqual(committed, ["P", "inbox"], "the scope is set before the view asks for the listings it needs");
vm.runInContext('setScope("Q")', tiles);
vm.runInContext("dropMissingScope()", tiles);
assert.equal(tiles.app.scope, "Q", "an archived project chosen from More stays the scope");
vm.runInContext('setScope("P")', tiles);
tiles.app.state.projects = [{ id: "P", archived: true }];
vm.runInContext("dropMissingScope()", tiles);
assert.ok(tiles.app.scope === "all" && tiles.app.selectedProject === null, "archiving the scoped project drops back to All and closes its page");
vm.runInContext('setScope("P")', tiles);
tiles.app.state.projects = [{ id: "Q" }];
vm.runInContext("dropMissingScope()", tiles);
assert.equal(tiles.app.scope, "all", "a project that is gone drops back to All");
console.log("dashboard session scope ok");
