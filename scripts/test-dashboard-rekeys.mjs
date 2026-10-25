import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";

const input = { value: "session_old" };
const requests = [];
const context = {
  app: { rekeyed: {}, drafts: new Map([["session_old", "unsent"]]), voice: { requestedTarget: "session_old" } },
  $: (selector) => (selector === "#voice-target" ? input : null),
  voiceRequest: async (action, body) => { requests.push([action, body?.targetId]); },
  ensureVoicePeer: async () => {},
  window: { agenthailVoice: { start: async () => {} } },
  crypto: { randomUUID: () => "attempt" },
  toast: (message) => { throw new Error(message); },
};
await dashboardModules(["rekeys", "voice"], context);

context.applyRekeys({ session_old: "conversation-a" });
assert.equal(context.app.drafts.get("conversation-a"), "unsent");
assert(!context.app.drafts.has("session_old"));
assert.equal(context.app.voice.requestedTarget, "conversation-a");
assert.equal(input.value, "conversation-a");

input.value = "session_old";
await context.startVoice();
await context.transferVoice();
assert.deepEqual(requests.filter(([action]) => action === "select" || action === "transfer"), [["select", "conversation-a"], ["transfer", "conversation-a"]]);
console.log("Dashboard rekeys: drafts and voice targets follow the conversation id.");
