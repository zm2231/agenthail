import "./test-home.mjs";
import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";
import fs from "node:fs";

const context = {
  statusLabel: (status) => status,
  backgroundWaiting: () => false,
  timeAgo: () => "now",
};
await dashboardModules(["session-labels"], context);

assert.match(context.conversationMeta({ status: "idle", queueCount: 0 }, "gpt-luna"), / · gpt-luna$/);
assert.doesNotMatch(context.conversationMeta({ status: "idle", queueCount: 0 }), /gpt/);
assert.match(context.conversationMeta({ status: "idle", queueCount: 0, settings: { effort: "xhigh", permission: "plan" } }, "opus"), / · opus · effort xhigh · mode plan$/);
assert.doesNotMatch(context.conversationMeta({ status: "idle", queueCount: 0 }), /effort|mode/);
assert.match(context.conversationMeta({ status: "idle", queueCount: 0, settings: { model: "claude-opus-5-5" } }), / · claude-opus-5-5$/);

const composer = fs.readFileSync(new URL("../internal/daemon/dashboard/js/composer.js", import.meta.url), "utf8");
assert.doesNotMatch(composer, /pendingModel|From next turn/);
assert.match(composer, /command\?\.toLowerCase\(\) === "\/model" && result\?\.result\?\.output/);
assert.match(composer, /result\?\.result\?\.notice/);
console.log("dashboard model label tests passed");
