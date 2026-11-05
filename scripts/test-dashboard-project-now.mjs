import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import vm from "node:vm";

const vectors = JSON.parse(readFileSync(new URL("./fixtures/project-now-vectors.json", import.meta.url), "utf8"));
const context = { app: { state: {} } };
await dashboardModules(["project-now", "presence", "hosts"], context);
const projectNow = vm.runInContext("projectNow", context);

for (const vector of vectors.cases) {
  const sessions = vector.sessions.map((patch) => ({ ...vectors.base, ...patch }));
  const now = projectNow(sessions, vector.computers);
  assert.equal(now.showsGroups, vector.showsGroups, `${vector.name}: showsGroups`);
  assert.equal(now.count, sessions.length, `${vector.name}: count`);
  const got = now.groups.map((group) => ({ id: group.id, title: group.title, sessions: group.sessions.map((session) => session.id) }));
  assert.equal(JSON.stringify(got), JSON.stringify(vector.groups), vector.name);
}
console.log("dashboard project now tests passed");
