import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";
import * as tools from "../internal/daemon/dashboard/js/tool-activity.js";

const context = {};
await dashboardModules(["session-plan"], context);

const session = (fields) => ({ id: "s", status: "busy", open: true, plan: { done: 3, total: 5, current: "Running gates" }, ...fields });
assert.deepEqual({ ...context.livePlan(session({})) }, { done: 3, total: 5, current: "Running gates" }, "an open session part way through shows its progress");
assert.equal(context.planDetail(context.livePlan(session({}))), "3 of 5 tasks done · Running gates");
assert.equal(context.planDetail({ done: 2, total: 4 }), "2 of 4 tasks done");
assert.ok(context.livePlan(session({ status: "idle" })), "an idle open session between turns keeps its badge");
assert.equal(context.livePlan(session({ open: false, status: "idle" })), null, "a closed session that stopped part way shows no badge");
assert.equal(context.livePlan(session({ status: "offline" })), null, "an offline session shows no badge");
assert.equal(context.livePlan(session({ unavailableReason: "host offline" })), null, "an unreachable remote session shows no badge");
assert.equal(context.livePlan(session({ plan: { done: 5, total: 5 } })), null, "a finished list shows no badge");
assert.equal(context.livePlan(session({ plan: { done: 0, total: 0 } })), null, "an empty list shows no badge");
assert.equal(context.livePlan(session({ plan: undefined })), null, "a row from a daemon that sends no plan shows no badge");

assert.deepEqual({ ...tools.toolLine({ title: "TaskCreate", text: JSON.stringify({ subject: "Parse the list", description: "A long description" }) }) }, { verb: "Add task", object: "Parse the list", code: false });
assert.deepEqual({ ...tools.toolLine({ title: "TaskUpdate", text: JSON.stringify({ taskId: "2", status: "in_progress" }) }) }, { verb: "Update task", object: "Task 2 · in progress", code: false });
assert.equal(tools.toolLine({ title: "send_email", text: JSON.stringify({ subject: "Hi", description: "Weekly note" }) }).object, "Weekly note", "other tools keep their description");
console.log("dashboard plan ok");
