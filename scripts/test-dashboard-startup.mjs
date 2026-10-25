import assert from "node:assert/strict";
import { dashboardModules } from "./dashboard-modules.mjs";

for (const target of [null, "first", "second"]) {
  const loads = [];
  const selections = [];
  const listeners = new Map();
  const context = {
    $: () => ({ addEventListener() {} }),
    load: async (...args) => { loads.push(args); },
    selectSession: async (...args) => { selections.push(args); },
    renderSessions() {}, resizeComposer() {}, syncConversationLayout() {},
    showView() {}, startLiveStream() {}, stopLiveStream() {},
    bindVoiceEvents() {}, bindConversationEvents() {}, bindComposerEvents() {},
    bindChatEvents() {}, bindOperationEvents() {}, bindRequestEvents() {},
    bindSessionEvents() {}, bindStartEvents() {}, bindSettingsEvents() {},
    bindPushEvents() {}, bindClaudeSettingsEvents() {}, bindSigninEvents() {},
    bindDeviceEvents() {}, bindAuditEvents() {}, bindProjectEvents() {},
    bindSessionOperationEvents() {},
    window: { addEventListener() {} },
    document: { addEventListener() {} },
    location: { search: target ? `?session=${target}` : "", hash: "#conversations" },
    history: { replaceState() {} },
    navigator: { serviceWorker: {
      addEventListener: (name, callback) => listeners.set(name, callback),
      startMessages() {},
    } },
    URLSearchParams,
    setInterval() {},
  };
  await dashboardModules(["main"], context);
  assert.deepEqual(loads, target ? [[false, false]] : [[]]);
  assert.deepEqual(selections, target ? [[target, true]] : []);
  listeners.get("message")({ data: { type: "open-session", sessionId: "pushed" } });
  await new Promise(setImmediate);
  assert.deepEqual(loads.at(-1), [false, false]);
  assert.deepEqual(selections.at(-1), ["pushed", true]);
}
console.log("Dashboard startup: ordinary, URL and service-worker selection paths passed.");
