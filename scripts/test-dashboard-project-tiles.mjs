import "./test-home.mjs";
import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import vm from "node:vm";

const vectors = JSON.parse(readFileSync(new URL("./fixtures/decision-project-vectors.json", import.meta.url), "utf8"));
const context = { app: { state: {} } };
await dashboardModules(["decisions", "project-tiles", "inbox-stops"], context);
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
assert.deepEqual(ids(roomy.shown), ["newest", "middle", "old", "lead-only", "empty"], "a lead ranks with active projects, and an idle project fills a tile left over");
assert.deepEqual(ids(roomy.more), ["shelved"], "an archived project only appears under more");
assert.equal(tileLetter("agenthail") + tileLetter("_tools") + tileLetter(""), "AT·", "a tile's letter is the name's first letter or digit");
const colours = JSON.parse(readFileSync(new URL("./fixtures/project-colour-vectors.json", import.meta.url), "utf8"));
const projectColour = vm.runInContext("projectColour", context);
assert.equal(vm.runInContext("projectColourCount", context), colours.count, "the dashboard has as many project colours as the vectors");
for (const vector of colours.cases) assert.equal(projectColour(vector.id), vector.colour, `${vector.id} takes the same colour as on the Mac`);
const projectColourID = vm.runInContext("projectColourID", context);
for (const vector of colours.colourIds) {
  const id = projectColourID(vector.id, vector.owner);
  assert.equal(id, vector.colourId, vector.name);
  assert.equal(projectColour(id), vector.colour, vector.name);
}
const order = JSON.parse(readFileSync(new URL("./fixtures/tile-order-vectors.json", import.meta.url), "utf8"));
const leadState = vm.runInContext("leadState", context);
for (const vector of order.cases) {
  const waiting = {};
  const leads = {};
  const listed = vector.projects.map((project) => {
    if (project.waiting) waiting[project.id] = project.waiting;
    if (project.lead?.session) leads[project.id] = leadState(project.lead.session);
    return { id: project.id, displayName: project.id, sessionCount: project.sessionCount, lastActive: project.lastActive, archived: project.archived, coordinators: project.lead ? [{ name: "lead", surface: "claude" }] : undefined };
  });
  const tiles = orderTiles(listed, vector.limit, { waiting, leads, now: Date.parse(order.now) });
  assert.deepEqual(ids(tiles.shown), vector.shown, `${vector.name}: shown`);
  assert.deepEqual(ids(tiles.more), vector.more, `${vector.name}: more`);
}
const tileLeads = vm.runInContext("tileLeads", context);
const found = tileLeads([
  { id: "near", coordinators: [{ name: "lead", surface: "claude", sessionId: "s1", host: { id: "studio", state: "local" } }] },
  { id: "far", coordinators: [{ name: "lead", surface: "claude", sessionId: "s2", host: { id: "buildbox", state: "connected" } }] },
  { id: "unstarted", coordinators: [{ name: "lead", surface: "claude" }] },
], [
  { id: "s1", currentReason: "lead", lastActive: "2026-11-08T10:00:00Z" },
  { id: "buildbox::s2", currentReason: "working", lastActive: "2026-11-01T10:00:00Z" },
]);
assert.deepEqual(Object.keys(found).sort(), ["far", "near"], "a lead's session is found on its own computer and a paired one");
assert.equal(found.far.active, true, "a working lead is active");
assert.equal(found.near.active, false, "an idle lead is not");
const tileColumns = vm.runInContext("tileColumns", context);
const fillTiles = vm.runInContext("fillTiles", context);
assert.deepEqual([0, 120, 260, 312, 340].map(tileColumns), [3, 3, 5, 6, 6], "a column is at least 50 px, and there are always at least three");
assert.deepEqual([undefined, NaN, -40].map(tileColumns), [3, 3, 3], "an unmeasured row still gets three columns");
const many = Array.from({ length: 15 }, (_, n) => ({ id: `p${n}`, displayName: `p${n}`, sessionCount: 15 - n, lastActive: "2026-11-08T10:00:00Z" }));
for (const columns of [3, 5, 6]) {
  const filled = fillTiles(columns, (limit) => orderTiles(many, limit));
  assert.equal(2 + filled.shown.length + (filled.more.length ? 1 : 0), 2 * columns, `${columns} columns: Inbox, projects, more and All fill both rows`);
}
const mostlyIdle = many.map((project, n) => ({ ...project, sessionCount: n < 2 ? 1 : 0, lastActive: undefined }));
for (const columns of [3, 4, 5, 6]) {
  const filled = fillTiles(columns, (limit) => orderTiles(mostlyIdle, limit));
  assert.equal(3 + filled.shown.length, 2 * columns, `${columns} columns: idle projects fill the rows after the active ones`);
  assert.deepEqual(ids(filled.shown).slice(0, 2), ["p0", "p1"], "active projects come first");
}
const few = fillTiles(6, (limit) => orderTiles(many.slice(0, 10), limit));
assert.equal(few.shown.length, 10, "every project shows when they all fit, with no more tile");
assert.equal(few.more.length, 0, "no more tile when every project shows");
const stopVectors = JSON.parse(readFileSync(new URL("./fixtures/inbox-stop-vectors.json", import.meta.url), "utf8"));
const inboxStops = vm.runInContext("inboxStops", context);
const stopsByProject = vm.runInContext("stopsByProject", context);
for (const vector of stopVectors.cases) {
  const stops = inboxStops({ replacements: vector.replacements, decisions: vector.decisions });
  assert.equal(JSON.stringify(stops.stopped.map((stop) => stop.id)), JSON.stringify(vector.stopped), `${vector.name}: stopped`);
  assert.equal(JSON.stringify(stops.decisions.map((item) => item.sessionId)), JSON.stringify(vector.decisionsShown), `${vector.name}: decisions`);
  assert.equal(JSON.stringify(stopsByProject(stops, vector.projects, (id) => vector.sessions[id])), JSON.stringify(vector.counts), `${vector.name}: counts`);
  assert.equal(stops.count, vector.total, `${vector.name}: total`);
}
const css = readFileSync(new URL("../internal/daemon/dashboard/tokens.css", import.meta.url), "utf8");
for (let colour = 0; colour < colours.count; colour++) {
  assert.ok(css.includes(`.tile-mark[data-colour="${colour}"]`), `tokens.css paints project colour ${colour}`);
}
console.log("dashboard project tiles tests passed");
