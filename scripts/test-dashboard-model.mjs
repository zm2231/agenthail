import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";
import fs from "node:fs";

const context = {
  statusLabel: (status) => status,
  backgroundWaiting: () => false,
  timeAgo: () => "now",
};
await dashboardModules(["session-labels"], context);

assert.equal(context.modelLabel("gpt-luna", ""), "gpt-luna");
assert.equal(context.modelLabel("gpt-luna", "gpt-luna"), "gpt-luna");
assert.equal(context.modelLabel("gpt-luna", "gpt-sol"), "gpt-luna · gpt-sol from next turn");
assert.equal(context.modelLabel("", "gpt-sol"), "gpt-sol from next turn");

const session = { status: "idle", queueCount: 0, pendingModel: "gpt-sol" };
assert.match(context.conversationMeta(session, "gpt-luna"), /gpt-luna · gpt-sol from next turn/);
assert.match(context.conversationMeta(session, "gpt-luna", ""), /gpt-luna$/);
assert.doesNotMatch(context.conversationMeta({ status: "idle", queueCount: 0 }, "gpt-luna"), /from next turn/);

assert.match(fs.readFileSync(new URL("../internal/daemon/dashboard/js/composer.js", import.meta.url), "utf8"), /model\.id === \(app\.history\?\.pendingModel \|\| app\.selected\?\.pendingModel\) \? "From next turn"/);
assert.match(fs.readFileSync(new URL("../internal/daemon/dashboard/js/composer.js", import.meta.url), "utf8"), /command\?\.toLowerCase\(\) === "\/model" && result\?\.result\?\.output/);
assert.match(fs.readFileSync(new URL("../internal/daemon/dashboard/js/composer.js", import.meta.url), "utf8"), /result\?\.result\?\.notice/);
console.log("dashboard model label tests passed");
