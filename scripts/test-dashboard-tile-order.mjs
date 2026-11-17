import "./test-home.mjs";
import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";

function storage(initial = {}) {
  const items = new Map(Object.entries(initial));
  return { getItem: (key) => items.get(key) ?? null, setItem: (key, value) => items.set(key, String(value)), items };
}

function daemon(state) {
  const server = { state, writes: [] };
  server.fetch = async (path, { method, body }) => {
    assert.equal(path, "/api/v1/projects/tiles");
    assert.equal(method, "PUT");
    const sent = JSON.parse(body);
    server.writes.push(sent);
    if (server.offline) throw new TypeError("Failed to fetch");
    const reply = (status, value) => ({ status, ok: status < 300, json: async () => value });
    if (sent.revision !== server.state.revision) return reply(409, { error: { code: "conflict" }, tileOrder: server.state });
    server.state = { order: sent.order, pinned: sent.pinned, revision: server.state.revision + 1 };
    return reply(200, { tileOrder: server.state });
  };
  return server;
}

async function store(local, server) {
  const context = { localStorage: storage(local), fetch: (...args) => server.fetch(...args) };
  await dashboardModules(["tile-order"], context);
  return context;
}

const settle = () => new Promise((resolve) => setTimeout(resolve, 0));
const plain = (value) => JSON.parse(JSON.stringify(value));

{
  const server = daemon({ order: [], pinned: [], revision: 0 });
  const page = await store({ "agenthail.tileOrder": JSON.stringify({ order: ["c", "a", "b"], pinned: ["b"], revision: 0 }) }, server);
  page.seedTileOrder(["a", "b", "c"]);
  page.adoptTileOrder({ order: [], pinned: [], revision: 0 });
  await settle();
  assert.deepEqual(plain(server.state), { order: ["c", "a", "b"], pinned: ["b"], revision: 1 }, "a daemon with no order gets the page's own");
  page.adoptTileOrder(server.state);
  await settle();
  assert.equal(server.writes.length, 1, "and is sent it once");
}

{
  const server = daemon({ order: ["b", "a", "c"], pinned: [], revision: 4 });
  const page = await store({}, server);
  page.seedTileOrder(["a", "b", "c"]);
  page.adoptTileOrder(server.state);
  assert.deepEqual(plain(page.tileOrder().order), ["b", "a", "c"], "a page with only the ranking takes the daemon's order");
  assert.equal(server.writes.length, 0, "and writes nothing");
  page.tilesArrangedBy((arrangement) => arrangement.order);
  server.state = { order: ["c", "b", "a"], pinned: [], revision: 5 };
  await page.changeTiles({ kind: "move", id: "a", target: "b" }, ["b", "a", "c"]);
  assert.deepEqual(plain(server.state), { order: ["c", "a", "b"], pinned: [], revision: 6 }, "a move against an older order is made again on the one that replaced it");
  assert.deepEqual(plain(page.tileOrder().order), ["c", "a", "b"], "and shown");
}

{
  const server = daemon({ order: ["a", "b", "c"], pinned: [], revision: 2 });
  server.offline = true;
  const local = storage();
  const context = { localStorage: local, fetch: (...args) => server.fetch(...args) };
  await dashboardModules(["tile-order"], context);
  context.adoptTileOrder(server.state);
  await context.changeTiles({ kind: "pin", id: "c" }, ["a", "b", "c"]);
  assert.deepEqual(plain(context.tileOrder().pinned), ["c"], "a change made offline shows at once");
  const reopened = await store(Object.fromEntries(local.items), server);
  assert.deepEqual(plain(reopened.tileOrder().pinned), ["c"], "and is kept across reloads");
  server.offline = false;
  server.state = { order: ["b", "a", "c"], pinned: [], revision: 3 };
  reopened.adoptTileOrder(server.state);
  await settle();
  await settle();
  assert.deepEqual(plain(server.state.pinned), ["c"], "it is sent when the daemon answers, and the later write wins");
  assert.equal(server.state.revision, 4);
}

console.log("dashboard tile order tests passed");
