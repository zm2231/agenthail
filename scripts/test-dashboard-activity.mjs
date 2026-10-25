import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";

const context = { statusLabel: (status) => status, backgroundWaiting: () => false };
await dashboardModules(["presence"], context);

const now = Date.parse("2026-10-23T10:14:30Z");
const busy = (activity) => JSON.parse(JSON.stringify({ name: "builder", status: "busy", currentReason: "working", activity }));
const since = new Date("2026-10-23T10:02:00Z").toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });

assert.equal(
  context.presenceLabel(busy({ busySince: "2026-10-23T10:00:00Z", lastProgressAt: "2026-10-23T10:02:00Z", tool: { name: "Bash", startedAt: "2026-10-23T10:02:00Z" } }), now),
  `Working · no activity for 12 min · running Bash since ${since}`,
);
assert.equal(context.presenceLabel(busy({ lastProgressAt: "2026-10-23T10:05:00Z" }), now), "Working");
const codexSince = new Date("2026-10-23T10:01:00.5Z").toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
assert.equal(
  context.presenceLabel({ ...busy({ busySince: "2026-10-23T09:58:00.034Z", lastProgressAt: "2026-10-23T06:02:00.123456789-04:00", tool: { name: "exec", startedAt: "2026-10-23T10:01:00.5Z" } }), surface: "codex" }, now),
  `Working · no activity for 12 min · running exec since ${codexSince}`,
  "a quiet Codex turn reads the same way, whatever offset and precision its times carry",
);
assert.equal(context.presenceLabel(busy(undefined), now), "Working");
assert.equal(context.presenceLabel(busy({ lastProgressAt: "2026-10-23T05:07:57Z", noProcess: true }), now), "Working · no activity for 5 hr 6 min · no Claude process");
assert.equal(context.presenceLabel(busy({ lastProgressAt: "2026-10-23T08:14:00Z" }), now), "Working · no activity for 2 hr");
assert.equal(
  context.presenceLabel({ ...busy({ lastProgressAt: "2026-10-23T09:00:00Z" }), waitingOn: { kind: "question", id: "q", answerable: true } }, now),
  "Waiting on you",
  "a wait outranks the quiet note",
);
assert.equal(context.presenceLabel({ name: "builder", status: "idle", currentReason: "recent", activity: { lastProgressAt: "2026-10-23T09:00:00Z" } }, now), "Recent");
console.log("dashboard activity ok");
