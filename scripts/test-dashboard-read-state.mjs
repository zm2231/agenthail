import "./test-home.mjs";
import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";

const sent = [];
let refuse = null;
let timers = [];
const page = { visible: true, focused: true };
const context = {
  app: { selected: null, mobileChatOpen: false },
  location: { hash: "#conversations" },
  document: {
    get visibilityState() { return page.visible ? "visible" : "hidden"; },
    hasFocus: () => page.focused,
  },
  crypto: { randomUUID: () => "key" },
  setTimeout: (run) => timers.push(run),
  clearTimeout: (id) => { if (id) timers[id - 1] = null; },
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
const dwell = () => {
  const due = timers;
  timers = [];
  for (const run of due) run?.();
};
const settle = () => new Promise((resolve) => setImmediate(resolve));
const row = (id, revision, firstUnread = "turn-1") => ({ id, readState: { firstUnread, revision } });

context.app.selected = row("s1", "rev-1");
page.visible = false;
context.readWhileLooking(true);
page.visible = true;
page.focused = false;
context.readWhileLooking(true);
dwell();
assert.deepEqual(sent, [], "a conversation in a hidden or unfocused tab must stay unread");

page.focused = true;
context.readWhileLooking(true);
context.app.selected = row("s2", "rev-7", "");
context.readWhileLooking(true);
dwell();
assert.deepEqual(sent, [{ name: "session-read", sessionID: "s2", revision: "rev-7" }], "flicking past a conversation sends nothing; the one left open is read, and so loaded, even when it shows read");

context.app.selected = row("s1", "rev-1");
context.readWhileLooking(true);
page.focused = false;
dwell();
assert.equal(sent.length, 1, "a conversation the person looked away from within the dwell stays unread");
page.focused = true;
context.readWhileLooking(true);
dwell();
assert.deepEqual(sent.at(-1), { name: "session-read", sessionID: "s1", revision: "rev-1" });
context.readWhileLooking(true);
dwell();
assert.equal(sent.length, 2, "a revision is sent once");

context.app.selected = row("s1", "rev-2", "");
context.readWhileLooking();
dwell();
assert.equal(sent.length, 2, "a row update for a read conversation sends nothing");

await context.markUnread(context.app.selected);
assert.deepEqual(sent.at(-1), { name: "session-unread", sessionID: "s1", revision: "rev-2" });
context.app.selected = row("s1", "rev-3", "threadStart");
context.readWhileLooking();
dwell();
assert.equal(sent.length, 3, "a conversation the person marked unread stays unread while they keep looking");
context.readWhileLooking(true);
dwell();
assert.deepEqual(sent.at(-1), { name: "session-read", sessionID: "s1", revision: "rev-3" }, "coming back to it marks it read");

refuse = 409;
context.app.selected = row("s1", "rev-4");
context.readWhileLooking();
dwell();
await settle();
refuse = null;
context.app.selected = row("s1", "rev-5");
context.readWhileLooking();
dwell();
assert.equal(sent.length, 5, "a refused mark is not retried at the newer revision without a fresh action");
context.readWhileLooking(true);
dwell();
assert.deepEqual(sent.at(-1), { name: "session-read", sessionID: "s1", revision: "rev-5" });

console.log("dashboard read state ok");
