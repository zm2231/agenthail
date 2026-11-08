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
  scopedSessions: () => (app.inboxMode === "projects" ? sessions.filter((session) => session.project) : sessions),
  orderReady: () => true,
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
app.scope = "inbox";
assert.equal(render(), "[inbox]", "the Inbox scope shows the decision groups");
app.scope = "all";
assert.ok(render().includes("[q1]"), "All shows every session again");
console.log("dashboard session scope ok");
