import "./test-home.mjs";
import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";

const box = { innerHTML: "", hidden: true, addEventListener(_, handler) { box.handler = handler; } };
const posted = [];
const reloaded = [];
const confirms = [];
const toasts = [];
let confirmAnswer = true;
let refuse = null;
const detail = {
  readOnly: false,
  turnSettings: [
    { key: "mode", value: "manual", applies: "now", options: [{ id: "manual", label: "Manual" }, { id: "plan", label: "Plan" }, { id: "bypassPermissions", label: "Bypass permissions", confirm: true }] },
    { key: "model", value: "gpt-sol", applies: "nextTurn", options: [{ id: "gpt-sol", label: "GPT Sol" }, { id: "gpt-luna", label: "GPT Luna" }] },
    { key: "effort", value: "medium", applies: "nextTurn", options: [{ id: "medium", label: "Medium" }, { id: "high", label: "High" }] },
  ],
};
const context = {
  $: (selector) => (selector === "#turn-settings" ? box : null),
  app: { selected: { id: "s1", surface: "codex" }, history: detail },
  window: { confirm: (text) => { confirms.push(text); return confirmAnswer; } },
  action: async (name, payload) => {
    posted.push({ name, ...payload });
    if (refuse) throw refuse;
    return { ok: true };
  },
  loadSessionMetadata: (id, from) => reloaded.push({ id, same: from === detail }),
  toast: (text) => toasts.push(text),
};
await dashboardModules(["turn-settings"], context);

context.renderTurnSettings();
assert.equal(box.hidden, false);
const order = [...box.innerHTML.matchAll(/data-turn-setting="(\w+)"/g)].map((match) => match[1]);
assert.deepEqual(order, ["model", "effort", "mode"], "chips show model, effort, then mode");
assert.match(box.innerHTML, /<option value="medium" selected>Medium<\/option>/);
assert.equal((box.innerHTML.match(/next turn/g) || []).length, 2, "settings that apply next turn say so");

context.bindTurnSettingEvents();
const change = (key, value) => box.handler({ target: { closest: () => ({ dataset: { turnSetting: key }, value }) } });

change("effort", "high");
await new Promise((resolve) => setTimeout(resolve, 0));
assert.deepEqual(posted, [{ name: "turn-setting", setting: "effort", value: "high" }], "choosing an effort posts turn-setting");
assert.deepEqual(reloaded, [{ id: "s1", same: true }], "the chips reread the session's metadata after a change");
assert.deepEqual(confirms, [], "a plain option needs no confirmation");

assert.equal(await context.changeTurnSetting("effort", "medium"), false, "the current value posts nothing");
assert.equal(posted.length, 1);

confirmAnswer = false;
assert.equal(await context.changeTurnSetting("mode", "bypassPermissions"), false);
assert.equal(confirms.length, 1, "bypass permissions asks first");
assert.match(confirms[0], /Bypass permissions/);
assert.equal(posted.length, 1, "a declined confirmation posts nothing");

confirmAnswer = true;
assert.equal(await context.changeTurnSetting("mode", "bypassPermissions"), true);
assert.deepEqual(posted.at(-1), { name: "turn-setting", setting: "mode", value: "bypassPermissions" }, "a confirmed option posts");

refuse = new Error("codex needs 0.133.0 or newer");
assert.equal(await context.changeTurnSetting("model", "gpt-luna"), false);
assert.match(toasts.at(-1), /0\.133\.0/, "a refused change says why");
refuse = null;

const compactModules = await dashboardModules(["compact-settings"], {});
const compactDetail = {
  ...detail,
  compactSettings: [
    { key: "autoCompact", value: "on", applies: "now", options: [{ id: "on", label: "On" }, { id: "off", label: "Off" }] },
    { key: "compactWindow", value: "", applies: "newSessions", scope: "work", options: [{ id: "auto", label: "Auto" }, { id: "400000", label: "400k" }] },
  ],
};
const panel = compactModules["compact-settings"].renderCompactSettings(compactDetail);
assert.deepEqual([...panel.matchAll(/data-turn-setting="(\w+)"/g)].map((match) => match[1]), ["autoCompact", "compactWindow"], "session info shows auto-compact and its window");
assert.match(panel, /For new sessions on work/, "a root window says it is for new sessions on that root");
assert.match(panel, /<option value="" selected disabled>Default<\/option>/, "an unset window reads as the default");
assert.equal(compactModules["compact-settings"].renderCompactSettings({ ...compactDetail, readOnly: true }), "", "a read-only session shows no compact settings");
assert.equal(compactModules["compact-settings"].renderCompactSettings(detail), "", "a session without compact settings shows none");
assert.equal(compactModules["compact-settings"].compactSettingsOf(detail).length, 0, "a session without compact settings has none to change");
const timelineModules = await dashboardModules(["timeline"], {});
const signature = timelineModules.timeline.timelineSignature;
const session = { id: "s1", status: "idle" };
assert.equal(signature(session, [], null, {}, "", [], [], [], 0, false, false), signature(session, [], null, {}, "", [], [], [], 0, false, false, []), "a session without compact settings keeps the transcript signature it had");
context.app.history = compactDetail;
assert.equal(await context.changeTurnSetting("compactWindow", "400000"), true);
assert.deepEqual(posted.at(-1), { name: "turn-setting", setting: "compactWindow", value: "400000" }, "a compact window posts turn-setting");
assert.equal(await context.changeTurnSetting("autoCompact", "on"), false, "the current compact value posts nothing");

context.app.history = { readOnly: true, turnSettings: detail.turnSettings };
context.renderTurnSettings();
assert.equal(box.hidden, true, "a read-only session shows no chips");
context.app.history = { turnSettings: [{ key: "mode", value: "default", applies: "nextTurn", options: [{ id: "default", label: "Default" }] }] };
context.renderTurnSettings();
assert.deepEqual([...box.innerHTML.matchAll(/data-turn-setting="(\w+)"/g)].map((match) => match[1]), ["mode"], "settings the session does not report are hidden");
console.log("dashboard turn settings tests passed");
