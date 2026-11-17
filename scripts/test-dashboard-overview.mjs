import "./test-home.mjs";
import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";
import vm from "node:vm";

const context = {};
await dashboardModules(["overview-counts"], context);
const overviewLine = vm.runInContext("overviewLine", context);
const overviewWorking = vm.runInContext("overviewWorking", context);
const workingNow = vm.runInContext("workingNow", context);

const running = { running: true };
const surfaces = [{ connected: true }, { connected: false }];
const sessions = [
  { currentReason: "working", queueCount: 2 },
  { currentReason: "working", waitingOn: { kind: "question" }, queueCount: 1 },
  { currentReason: "recent", queueCount: 0 },
];

assert.equal(workingNow(sessions[0]), true);
assert.equal(workingNow(sessions[1]), false, "a session waiting on the person is not working");
assert.equal(overviewWorking({ workingFamilies: 4, attentionFamilies: 1 }, sessions), 4, "the daemon's working families are the working number");
assert.equal(overviewWorking(undefined, sessions), 1, "without a summary, sessions waiting on the person are left out");
assert.equal(
  overviewLine({ daemon: running, surfaces, sessions, working: 1, needsYou: 2 }),
  "1 agent app connected · 1 working · 2 need you · 3 messages queued",
  "the line gives working and need-you once each, like the Mac",
);
assert.equal(overviewLine({ daemon: running, surfaces: [], sessions: [], working: 0, needsYou: 1 }), "No agent apps connected · 1 needs you");
assert.equal(overviewLine({ daemon: { running: true, stale: true, refreshError: "Codex timed out." }, surfaces, sessions, working: 0, needsYou: 0 }), "Showing cached data. Codex timed out.");
assert.equal(overviewLine({ daemon: { running: false }, surfaces, sessions, working: 0, needsYou: 0 }), "Start Agenthail to deliver work.");
console.log("dashboard overview ok");
