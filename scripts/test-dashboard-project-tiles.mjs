import "./test-home.mjs";
import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import vm from "node:vm";

const vectors = JSON.parse(readFileSync(new URL("./fixtures/decision-project-vectors.json", import.meta.url), "utf8"));
const context = { app: { state: {} } };
await dashboardModules(["decisions", "project-tiles"], context);
const qualify = vm.runInContext("qualify", context);
const projectOfDecision = vm.runInContext("projectOfDecision", context);
const needsYouByProject = vm.runInContext("needsYouByProject", context);
const orderTiles = vm.runInContext("orderTiles", context);
const tileLetter = vm.runInContext("tileLetter", context);

const decisions = vectors.cases.map((vector) => {
  const item = { hostId: vector.host, sessionId: qualify(vector.host, vector.session) };
  if (vector.projectId) item.projectId = qualify(vector.host, vector.projectId);
  assert.equal(projectOfDecision(item, vectors.projects), vector.tile, vector.name);
  return item;
});
assert.equal(JSON.stringify(needsYouByProject(decisions, vectors.projects)), JSON.stringify(vectors.counts), "tile badges count their decisions");
assert.equal(decisions.length, vectors.inboxTotal, "the Inbox still counts decisions no tile holds");

const projects = [
  { id: "old", displayName: "old", sessionCount: 2, lastActive: "2026-11-01T10:00:00Z" },
  { id: "newest", displayName: "newest", sessionCount: 1, lastActive: "2026-11-08T10:00:00Z" },
  { id: "lead-only", displayName: "lead-only", sessionCount: 0, coordinators: [{ name: "lead", surface: "claude" }] },
  { id: "empty", displayName: "empty", sessionCount: 0 },
  { id: "shelved", displayName: "shelved", sessionCount: 4, lastActive: "2026-11-08T11:00:00Z", archived: true },
  { id: "middle", displayName: "middle", sessionCount: 3, lastActive: "2026-11-05T10:00:00Z" },
];
const ids = (list) => list.map((project) => project.id);
const tight = orderTiles(projects, 3);
assert.deepEqual(ids(tight.shown), ["newest", "middle", "old"], "tiles are the most recently active open projects");
assert.deepEqual(ids(tight.more), ["empty", "lead-only", "shelved"], "the rest, archived ones included, go under more");
const roomy = orderTiles(projects, 6);
assert.deepEqual(ids(roomy.shown), ["newest", "middle", "old", "lead-only"], "a project with no sessions gets a tile when it has a lead");
assert.deepEqual(ids(roomy.more), ["empty", "shelved"], "an archived project only appears under more");
assert.equal(tileLetter("agenthail") + tileLetter("_tools") + tileLetter(""), "AT·", "a tile's letter is the name's first letter or digit");
console.log("dashboard project tiles tests passed");
