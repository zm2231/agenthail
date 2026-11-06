import assert from "node:assert/strict";
import { dashboardModules } from "./dashboard-modules.mjs";

const family = (id, projectId = "p") => ({ id, rootId: id, projectId, members: [{ id, depth: 0, contextOnly: false }] });
const row = (id) => ({ id, name: id });
const view = (families, fields = {}) => ({ projects: [{ id: "p", collapsedFamilyIds: families.map((item) => item.id) }], families, refreshAt: null, revision: "r1", counts: { all: { sessions: 3 }, archived: { sessions: 1 } }, ...fields });
const page = (scope, families, fields = {}) => ({ hostEpoch: "e1", counts: { current: 1, all: 3 }, view: view(families, { scope, ...fields.view }), sessions: families.map((item) => row(item.id)), ...fields.page });
const path = (params) => `/api/state?${new URLSearchParams(params)}`;
let pages = {};
const serve = () => {
  pages = {
    "/api/state?sessions=none": { hostEpoch: "e1", daemon: { running: true } },
    [path({ scope: "current", members: "1", limit: "200" })]: page("current", [family("b")], { view: { refreshAt: "2026-11-05T12:10:00Z" } }),
    [path({ scope: "all", members: "1", limit: "2" })]: page("all", [family("b"), family("a")], { page: { nextCursor: "next" }, view: { refreshAt: "2026-11-05T12:05:00Z" } }),
    [path({ scope: "all", members: "1", limit: "4" })]: page("all", [family("b"), family("a")], { page: { nextCursor: "next" } }),
    [path({ scope: "all", members: "1", limit: "4", cursor: "next" })]: page("all", [family("a"), family("c")]),
    [path({ scope: "all", members: "1", surface: "codex", status: "working", q: "fix", limit: "2" })]: page("all", [family("c")]),
    [path({ scope: "all", members: "1", collapsed: "1", limit: "200" })]: page("all", [family("b"), family("a"), family("c")]),
    [path({ scope: "all", members: "1", collapsed: "1", q: "fix", limit: "200" })]: page("all", [family("c")]),
    [path({ scope: "archived", members: "1", collapsed: "1", limit: "200" })]: page("archived", [family("z")]),
    [path({ scope: "archived", members: "1", collapsed: "1", q: "fix", limit: "200" })]: page("archived", []),
    [path({ scope: "all", members: "1", projectId: "p", limit: "200" })]: page("all", [family("a")]),
  };
};
serve();
let requested = [];
const context = {
  URLSearchParams,
  app: { view: "conversations", inboxMode: "current", showArchived: false, selectedProject: null, filters: { surface: "all", status: "all" }, listQuery: "", sessionLimit: 2 },
  fetch: async (url) => {
    requested.push(url);
    if (!pages[url]) return { ok: false, status: 404 };
    return { ok: true, status: 200, json: async () => structuredClone(pages[url]) };
  },
  responseError: async (response) => new Error(`refused ${response.status}`),
};
await dashboardModules(["session-order"], context);
const { app } = context;
const ids = (key) => context.orderedSessions(key).map((session) => session.id).join(",");

let { base, refreshAt } = await context.loadSessionOrder();
assert.deepEqual(requested.sort(), ["/api/state?sessions=none", path({ scope: "current", members: "1", limit: "200" })].sort(), "the Current list reads the base and Current, nothing else");
assert.equal(base.daemon.running, true);
assert.equal(refreshAt, Date.parse("2026-11-05T12:10:00Z"));
assert.deepEqual([...context.listedRows().keys()], ["b"], "rows come from the pages read");
assert.equal(context.orderIsMissing(), false);

app.inboxMode = "all";
assert.equal(context.orderIsMissing(), true, "a list that shows All needs All read");
assert.equal(context.orderReady(["all"]), false, "a list never read is not ready to render");
for (const shown of ["overview", "operations"]) {
  app.view = shown;
  assert.deepEqual([...context.neededOrderScopes()], ["current"], `a hidden list is not read while ${shown} is shown`);
}
app.view = "conversations";
requested = [];
({ refreshAt } = await context.loadSessionOrder());
assert(requested.includes(path({ scope: "all", members: "1", limit: "2" })) && !requested.some((url) => url.includes("cursor")), "All reads only the page the list shows");
assert.equal(refreshAt, Date.parse("2026-11-05T12:05:00Z"), "the earliest refresh deadline across the listings is returned");
assert.equal(ids("all"), "b,a");
assert.equal(context.listingHasMore("all"), true);
assert.equal(context.listedTotal("all"), 3, "the total comes from the daemon's count, not the rows read");
assert.equal(context.orderIsMissing(), false, "a list showing every row it asked for needs nothing more");

app.sessionLimit = 4;
assert.equal(context.orderIsMissing(), true, "showing more needs the next page");
requested = [];
await context.loadSessionOrder();
assert(requested.includes(path({ scope: "all", members: "1", limit: "4", cursor: "next" })));
assert.equal(ids("all"), "b,a,c", "a family repeated on a later page is listed once, where it first appeared");
assert.equal(context.listingHasMore("all"), false);

app.sessionLimit = 2;
Object.assign(app.filters, { surface: "codex", status: "working" });
app.listQuery = "fix";
assert.equal(context.orderIsMissing(), true, "a changed refinement is read from the daemon");
assert.equal(context.orderReady(["all"]), true, "the previous results stay on screen while the refinement is read");
await context.loadSessionOrder();
assert.equal(ids("all"), "c", "the daemon filters and searches the list");
Object.assign(app.filters, { surface: "all", status: "all" });
app.listQuery = "";

app.inboxMode = "projects";
assert.deepEqual([...context.neededOrderScopes()], ["current", "all", "archived"]);
requested = [];
await context.loadSessionOrder();
assert(requested.includes(path({ scope: "all", members: "1", collapsed: "1", limit: "200" })), "the projects list reads only each project's collapsed families");
assert.equal(ids("all"), "b,a,c");
app.listQuery = "fix";
assert.equal(context.orderIsMissing(), true, "a search of the projects list goes to the daemon");
await context.loadSessionOrder();
assert.equal(ids("all"), "c");
app.listQuery = "";
app.state = { projects: [{ id: "p" }] };
app.expandedProjects = new Set(["p"]);
assert.deepEqual([...context.neededOrderScopes()], ["current", "all", "archived", "project:all:p"], "an expanded project reads its own listing");
app.expandedProjects = new Set();
app.selectedProject = "p";
assert.deepEqual([...context.neededOrderScopes()], ["current", "all", "archived", "project:all:p"], "an open project reads its own listing");
await context.loadSessionOrder();
assert.equal(context.projectSessions("project:all:p", "p").map((session) => session.id).join(","), "a");
app.view = "overview";
assert.deepEqual([...context.neededOrderScopes()], ["current"], "a project left open behind Overview is not read");

await context.loadSessionOrder();
assert.deepEqual(Object.keys(app.sessionOrder), ["current"], "listings no longer shown are dropped");
assert.deepEqual([...context.listedRows().keys()], ["b"], "rows only a dropped listing held are evicted with it");
app.view = "conversations";
app.selectedProject = null;
app.inboxMode = "all";
assert.equal(context.orderIsMissing(), true, "returning to All reads it again");

pages["/api/state?sessions=none"].hostEpoch = "e2";
await assert.rejects(context.loadSessionOrder(), /restarted/, "a base and pages from different daemon runs are never mixed");
let flips = 0;
const flipping = context.fetch;
context.fetch = async (url) => {
  if (url === "/api/state?sessions=none") pages[url].hostEpoch = flips++ === 0 ? "e2" : "e1";
  return flipping(url);
};
await context.loadSessionOrder();
assert.equal(flips, 2, "a restart between reads is read again");
context.fetch = flipping;
serve();
app.sessionLimit = 4;
pages[path({ scope: "all", members: "1", limit: "4", cursor: "next" })].hostEpoch = "e2";
await assert.rejects(context.loadSessionOrder(), /restarted/, "a listing whose later page comes from another daemon run is never used");
serve();
await context.loadSessionOrder();
assert.equal(ids("all"), "b,a,c");
app.sessionLimit = 2;

context.fetch = async () => ({ ok: false, status: 500 });
await assert.rejects(context.loadSessionOrder(), /refused/, "a refused view fails the load rather than showing a partial order");

const lookups = {
  [path({ scope: "all", members: "1", limit: "1" })]: { view: { revision: "ra" } },
  [path({ scope: "archived", members: "1", limit: "1" })]: { view: { revision: "rz" } },
  [path({ scope: "all", members: "1", revision: "ra", sessionId: "deep" })]: { status: 400 },
  [path({ scope: "archived", members: "1", revision: "rz", sessionId: "deep" })]: { sessions: [row("deep")] },
};
const finder = { URLSearchParams, fetch: async (url) => (lookups[url]?.status ? { ok: false, status: lookups[url].status } : { ok: true, status: 200, json: async () => lookups[url] }), responseError: async () => new Error("refused") };
await dashboardModules(["session-pages"], finder);
assert.equal((await finder.findSession("deep")).id, "deep", "a session outside the shown lists is read by id, archived ones too");
lookups[path({ scope: "all", members: "1", revision: "ra", sessionId: "near" })] = { sessions: [row("near")] };
lookups[`/api/state?${new URLSearchParams([["scope", "all"], ["members", "1"], ["revision", "ra"], ["sessionId", "near"], ["sessionId", "deep"]])}`] = { status: 400 };
const both = await finder.findSessions(["near", "deep"]);
assert.deepEqual([...both.keys()].sort(), ["deep", "near"], "a batch the daemon refuses is read one id at a time, then in Archived");
lookups[path({ scope: "archived", members: "1", revision: "rz", sessionId: "deep" })] = { status: 400 };
assert.equal(await finder.findSession("deep"), null, "a session no view holds is not found");
console.log("dashboard session order tests passed");
