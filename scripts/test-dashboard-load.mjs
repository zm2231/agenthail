import "./test-home.mjs";
import assert from "node:assert/strict";
import { dashboardModules } from "./dashboard-modules.mjs";

const elements = new Map();
const calls = [];
const errors = [];
let releaseTools;
let releaseState;
let rendered = 0;
let orderReads = 0;
let missingOrder = 0;
let rows = new Map();
let readOrder = (fresh) => { calls.push(fresh ? "order fresh" : "order"); return new Promise((resolve) => { releaseState = resolve; }); };
let loadRenderer = () => { calls.push("tools"); return new Promise((resolve) => { releaseTools = resolve; }); };
const selected = [];
const found = [];
let unconfirmed = [];
const order = (sessions, base = {}) => {
  rows = new Map(sessions.map((session) => [session.id, session]));
  context.app.sessionOrder = { current: { counts: { current: sessions.length } } };
  return { base: { daemon: { running: true }, ...base }, refreshAt: Infinity };
};
const context = {
  app: { loading: false, hasState: false, audit: {} },
  loadTools: () => loadRenderer(),
  loadSessionOrder: (fresh) => { orderReads++; return readOrder(fresh); },
  readHistory: async () => { calls.push("history"); return [{ id: 7 }]; },
  listedRows: () => new Map(rows),
  findSessions: async (ids) => { found.push(...ids); return new Map(ids.map((id) => [id, { id, name: "deep" }])); },
  unconfirmedSendsOutside: (known) => unconfirmed.filter((item) => !known(item.sessionId)),
  orderIsMissing: () => missingOrder-- > 0,
  $: (selector) => { if (!elements.has(selector)) elements.set(selector, {}); return elements.get(selector); },
  renderOverview: () => { rendered++; },
  renderSessions: () => {},
  renderSurfaceFilter: () => {},
  renderStartSurfaceOptions: () => {},
  renderOperations: () => {},
  refreshShownProject: () => {},
  mobileConversationView: () => false,
  scopedSessions: () => context.app.state.sessions,
  selectSession: async (id) => selected.push(id),
  applyRekeys: () => {},
  setTimeout: () => 1,
  clearTimeout: () => {},
  location: { hash: "#overview" },
  friendlyError: (error) => error.message,
  toast: (message) => errors.push(message),
};
await dashboardModules(["dashboard-state"], context);
const first = context.load();
assert.deepEqual(calls, ["tools", "order", "history"], "tools, the base with its listings, and history are read together");
await context.load();
await context.load();
assert.equal(calls.length, 3, "overlapping refreshes do not start duplicate requests");
releaseState(order([{ id: "one" }]));
await new Promise(setImmediate);
assert.equal(rendered, 0, "rendering requires the renderer to be ready");
releaseTools();
await first;
assert.equal(rendered, 1);
assert.equal(context.app.state.sessions[0].id, "one");
assert.deepEqual(context.app.state.counts, { current: 1 }, "list counts come from the daemon's view");
assert.deepEqual(context.app.state.history, [{ id: 7 }]);
assert.equal(calls.length, 6, "the refreshes asked for during a load run once after it");
releaseState(order([{ id: "one" }]));
releaseTools();
await new Promise(setImmediate);
await new Promise(setImmediate);
assert.equal(calls.length, 6);
assert.equal(context.app.loading, false);

loadRenderer = async () => {};
let failNext = true;
let followUp = null;
readOrder = () => new Promise((resolve, reject) => {
  if (failNext) {
    failNext = false;
    followUp = () => reject(new Error("daemon unreachable"));
    return;
  }
  resolve(order([{ id: "filtered" }]));
});
const background = context.load(false, false);
await new Promise(setImmediate);
await context.load();
followUp();
await background;
for (let i = 0; i < 5; i++) await new Promise(setImmediate);
assert.equal(errors.at(-1), "daemon unreachable");
assert.equal(context.app.state.sessions[0].id, "filtered", "a read the person asked for during a background load that then failed still runs, on the new list state");
assert.equal(context.app.loading, false);

readOrder = async () => order([]);
loadRenderer = async () => { throw new Error("module unavailable"); };
await context.load();
assert.equal(errors.at(-1), "module unavailable");
assert.equal(context.app.loading, false);
assert.equal(context.app.state.sessions[0].id, "filtered", "a failed load keeps the last state");
loadRenderer = async () => {};
readOrder = async () => { throw new Error("request failed"); };
await context.load();
assert.equal(errors.at(-1), "request failed");
readOrder = async (fresh) => { assert.equal(fresh, true); return order([{ id: "two" }]); };
const renderedBefore = rendered;
await context.load(true);
assert.equal(context.app.state.sessions[0].id, "two");
assert.equal(rendered, renderedBefore + 1, "a failed load renders nothing; the next good one renders once");

context.app.selected = { id: "two" };
await context.load(true);
assert.deepEqual(found, [], "a selection the listings hold is not read again");
context.app.selected = { id: "deep" };
await context.load(true);
assert.deepEqual(found, ["deep"], "a selection outside every shown list is refreshed by id");
assert.equal(context.app.selected.name, "deep");
assert(context.app.state.sessions.some((session) => session.id === "deep"));
context.app.selected = null;
found.length = 0;
unconfirmed = [{ sessionId: "two" }, { sessionId: "elsewhere" }, { sessionId: "elsewhere" }];
await context.load(true);
assert.deepEqual(found, ["elsewhere"], "the target of an unconfirmed send stays known though no shown list holds it");
assert(context.app.state.sessions.some((session) => session.id === "elsewhere"));
unconfirmed = [];

context.location.hash = "#conversations";
readOrder = async () => order([{ id: "first" }]);
await context.load(false, false);
assert.equal(selected.length, 0, "a link opens its target without opening the default session first");
await context.load();
assert.deepEqual(selected, ["first"], "ordinary navigation still opens its default conversation");
const before = orderReads;
missingOrder = 1;
await context.load(false, false);
await new Promise(setImmediate);
await new Promise(setImmediate);
assert.equal(orderReads - before, 2, "a view that needs a listing the finished load did not read gets one more read");
assert.equal(context.app.loading, false);
console.log("Dashboard load: parallel requests, render barrier, refresh deduplication and failure recovery passed.");
