import assert from "node:assert/strict";
import { dashboardModules } from "./dashboard-modules.mjs";

const family = (id) => ({ id, rootId: id, projectId: "p", members: [{ id, depth: 0, contextOnly: false }] });
const pages = {
  "/api/state?scope=current&limit=200": { view: { projects: [{ id: "p", collapsedFamilyIds: ["b"] }], families: [family("b")], refreshAt: "2026-11-05T12:10:00Z" } },
  "/api/state?scope=all&limit=200": { view: { projects: [{ id: "p", collapsedFamilyIds: ["b", "a"] }], families: [family("b")], refreshAt: "2026-11-05T12:05:00Z" }, nextCursor: "next page" },
  "/api/state?scope=all&limit=200&cursor=next%20page": { view: { projects: [], families: [family("b"), family("a")], refreshAt: null } },
  "/api/state?scope=archived&limit=200": { view: { projects: [], families: [], refreshAt: null } },
};
const requested = [];
const context = {
  app: { state: { sessions: [{ id: "a" }, { id: "b" }] } },
  fetch: async (path) => {
    requested.push(path);
    return { ok: true, json: async () => pages[path] };
  },
  responseError: async () => new Error("refused"),
};
await dashboardModules(["session-order"], context);
const refreshAt = await context.loadSessionOrder();
assert.deepEqual(requested.sort(), Object.keys(pages).sort(), "every scope is read, following its cursor to the end");
assert.equal(refreshAt, Date.parse("2026-11-05T12:05:00Z"), "the earliest refresh deadline across scopes is returned");
assert.equal(context.orderedSessions("all").map((row) => row.id).join(","), "b,a", "a family repeated on a later page is listed once, where it first appeared");
assert.equal(context.projectSessions("all", "p", true).map((row) => row.id).join(","), "b,a");
assert.deepEqual(context.orderedProjectIDs("all"), ["p"], "the project order comes from the first page");
context.fetch = async () => ({ ok: false });
await assert.rejects(context.loadSessionOrder(), /refused/, "a refused view fails the load rather than showing a partial order");
console.log("dashboard session order tests passed");
