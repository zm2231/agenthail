import "./test-home.mjs";
import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";

const sent = [];
let refuse = null;
const page = { visible: true, focused: true };
const context = {
  app: { selected: null, mobileChatOpen: false },
  location: { hash: "#conversations" },
  document: {
    get visibilityState() { return page.visible ? "visible" : "hidden"; },
    hasFocus: () => page.focused,
  },
  crypto: { randomUUID: () => "key" },
  mobileConversationView: () => false,
  action: async (name, extra, _key, sessionID) => {
    sent.push({ name, sessionID, revision: extra.revision });
    if (refuse) {
      const error = Error("refused");
      error.status = refuse;
      throw error;
    }
    return { ok: true };
  },
};
await dashboardModules(["read-state"], context);
const settle = () => new Promise((resolve) => setImmediate(resolve));
const row = (revision, firstUnread = "turn-1") => ({ id: "s1", readState: { firstUnread, revision } });

context.app.selected = row("rev-1");
page.visible = false;
context.readWhileLooking(true);
page.visible = true;
page.focused = false;
context.readWhileLooking(true);
assert.deepEqual(sent, [], "a conversation in a hidden or unfocused tab must stay unread");

page.focused = true;
context.readWhileLooking(true);
assert.deepEqual(sent, [{ name: "session-read", sessionID: "s1", revision: "rev-1" }], "opening an unread conversation on screen marks it read at the revision shown");
context.readWhileLooking();
assert.equal(sent.length, 1, "a revision is marked once");

context.app.selected = row("rev-2", "");
context.readWhileLooking();
assert.equal(sent.length, 1, "a read conversation is not marked again");

await context.markUnread(context.app.selected);
assert.deepEqual(sent.at(-1), { name: "session-unread", sessionID: "s1", revision: "rev-2" });
context.app.selected = row("rev-3", "threadStart");
context.readWhileLooking();
assert.equal(sent.length, 2, "a conversation the person marked unread stays unread while they keep looking");
context.readWhileLooking(true);
assert.deepEqual(sent.at(-1), { name: "session-read", sessionID: "s1", revision: "rev-3" }, "coming back to it marks it read");

refuse = 409;
context.app.selected = row("rev-4");
context.readWhileLooking();
await settle();
refuse = null;
context.app.selected = row("rev-5");
context.readWhileLooking();
assert.equal(sent.length, 4, "a refused mark is not retried at the newer revision without a fresh action");
context.readWhileLooking(true);
assert.deepEqual(sent.at(-1), { name: "session-read", sessionID: "s1", revision: "rev-5" });

console.log("dashboard read state ok");
