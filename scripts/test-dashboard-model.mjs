import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";

const source = fs.readFileSync(new URL("../internal/daemon/dashboard/app.js", import.meta.url), "utf8");
const start = source.indexOf("function conversationMeta(");
const end = source.indexOf("function surfaceIcon(", start);
assert(start >= 0 && end > start);
const context = {
  statusLabel: (status) => status,
  backgroundWaiting: () => false,
  timeAgo: () => "now",
};
vm.runInNewContext(source.slice(start, end), context);

assert.equal(context.modelLabel("gpt-luna", ""), "gpt-luna");
assert.equal(context.modelLabel("gpt-luna", "gpt-luna"), "gpt-luna");
assert.equal(context.modelLabel("gpt-luna", "gpt-sol"), "gpt-luna · gpt-sol from next turn");
assert.equal(context.modelLabel("", "gpt-sol"), "gpt-sol from next turn");

const session = { status: "idle", queueCount: 0, pendingModel: "gpt-sol" };
assert.match(context.conversationMeta(session, "gpt-luna"), /gpt-luna · gpt-sol from next turn/);
assert.match(context.conversationMeta(session, "gpt-luna", ""), /gpt-luna$/);
assert.doesNotMatch(context.conversationMeta({ status: "idle", queueCount: 0 }, "gpt-luna"), /from next turn/);

assert.match(source, /model\.id === \(app\.history\?\.pendingModel \|\| app\.selected\?\.pendingModel\) \? "From next turn"/);
assert.match(source, /command\?\.toLowerCase\(\) === "\/model" && result\?\.result\?\.output/);
assert.match(source, /result\?\.result\?\.notice/);
console.log("dashboard model label tests passed");
