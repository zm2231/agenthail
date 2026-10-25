import assert from "node:assert/strict";
import { dashboardModules } from "./dashboard-modules.mjs";

const elements = new Map();
const calls = [];
const errors = [];
let releaseTools;
let releaseState;
let rendered = 0;
let loadRenderer = () => { calls.push("tools"); return new Promise((resolve) => { releaseTools = resolve; }); };
const selected = [];
const context = {
  app: { loading: false, hasState: false, audit: {} },
  loadTools: () => loadRenderer(),
  fetch: (path) => { calls.push(path); return new Promise((resolve) => { releaseState = resolve; }); },
  $: (selector) => { if (!elements.has(selector)) elements.set(selector, {}); return elements.get(selector); },
  renderOverview: () => { rendered++; },
  renderSessions: () => {},
  renderSurfaceFilter: () => {},
  renderStartSurfaceOptions: () => {},
  renderOperations: () => {},
  mobileConversationView: () => false,
  scopedSessions: () => context.app.state.sessions,
  selectSession: async (id) => selected.push(id),
  applyRekeys: () => {},
  setTimeout: () => 1,
  clearTimeout: () => {},
  location: { hash: "#overview" },
  friendlyError: (error) => error.message,
  toast: (message) => errors.push(message),
  responseError: async () => new Error("request failed"),
};
await dashboardModules(["dashboard-state"], context);
const first = context.load();
assert.deepEqual(calls, ["tools", "/api/state"]);
await context.load();
assert.equal(calls.length, 2, "overlapping refreshes do not start duplicate requests");
releaseState({ ok: true, json: async () => ({ sessions: [{ id: "one" }], daemon: { running: true } }) });
await new Promise(setImmediate);
assert.equal(rendered, 0, "rendering requires the renderer to be ready");
releaseTools();
await first;
assert.equal(rendered, 1);
assert.equal(context.app.state.sessions[0].id, "one");
assert.equal(context.app.loading, false);

loadRenderer = async () => { throw new Error("module unavailable"); };
context.fetch = async () => ({ ok: true, json: async () => ({ sessions: [] }) });
await context.load();
assert.equal(errors.at(-1), "module unavailable");
assert.equal(context.app.loading, false);
assert.equal(context.app.state.sessions[0].id, "one");
loadRenderer = async () => {};
context.fetch = async () => ({ ok: false });
await context.load();
assert.equal(errors.at(-1), "request failed");
context.fetch = async (path) => { assert.equal(path, "/api/state?fresh=1"); return { ok: true, json: async () => ({ sessions: [{ id: "two" }] }) }; };
await context.load(true);
assert.equal(context.app.state.sessions[0].id, "two");
assert.equal(rendered, 2);
context.location.hash = "#conversations";
context.fetch = async () => ({ ok: true, json: async () => ({ sessions: [{ id: "first" }] }) });
await context.load(false, false);
assert.equal(selected.length, 0, "a link opens its target without opening the default session first");
await context.load();
assert.deepEqual(selected, ["first"], "ordinary navigation still opens its default conversation");
console.log("Dashboard load: parallel requests, render barrier, refresh deduplication and failure recovery passed.");
