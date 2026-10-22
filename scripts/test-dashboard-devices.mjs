import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";

const source = fs.readFileSync(new URL("../internal/daemon/dashboard/app.js", import.meta.url), "utf8");
const start = source.indexOf("async function signinRequest(");
const end = source.indexOf("\n}\n", start) + 3;
const devicesStart = source.indexOf("async function loadDevices(");
const devicesEnd = source.indexOf("async function loadClaudeCompact(", devicesStart);
assert(start >= 0 && end > start && devicesStart >= 0 && devicesEnd > devicesStart);

const nodes = {};
const node = (selector) => (nodes[selector] ??= { innerHTML: "" });
const toasts = [];
const pending = [];
const context = {
  app: {},
  escape: (value) => String(value ?? "").replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;").replaceAll('"', "&quot;"),
  $: node,
  toast: (message) => toasts.push(message),
  confirm: () => true,
  setTimeout: (resolve) => resolve(),
  Date,
  JSON,
  fetch: (url, options = {}) => new Promise((resolve) => pending.push({ url, options, resolve })),
};
vm.runInNewContext(source.slice(start, end) + source.slice(devicesStart, devicesEnd), context);

const settle = () => new Promise((resolve) => setImmediate(resolve));
const reply = async (status, body) => {
  await settle();
  const call = pending.shift();
  assert(call, "a request was made");
  call.resolve({ ok: status < 400, status, text: async () => JSON.stringify(body) });
  await settle();
  return call;
};
const phone = { id: "device-1", name: "Test iPhone", scopes: ["read", "control"], pushEnabled: true, lastSeenAt: "2026-10-22T10:00:00Z" };
const host = { id: "device-2", name: "laptop", scopes: ["read", "control", "host"] };

const load = context.loadDevices();
assert.equal((await reply(200, { devices: [phone, host] })).url, "/api/v1/devices");
await load;
assert(node("#devices-list").innerHTML.includes("Test iPhone"), "a paired phone is listed");
assert(!node("#devices-list").innerHTML.includes("laptop"), "a paired host is not listed as a device");
assert(node("#device-pairing").innerHTML.includes("data-device-pair"), "the card offers to pair");

context.startDevicePairing();
const created = await reply(201, { id: "pairing-1", pairingURL: "agenthail://pair?endpoint=https%3A%2F%2Fserver.example.ts.net&secret=s", expiresAt: "2026-10-22T10:05:00Z", qr: "data:image/png;base64,AAAA" });
assert.equal(created.url, "/api/v1/pairings");
assert.equal(created.options.method, "POST");
assert.deepEqual(JSON.parse(created.options.body), { name: "", scopes: ["read", "control"], qr: true });
const shown = node("#device-pairing").innerHTML;
assert(shown.includes('src="data:image/png;base64,AAAA"'), "the code's QR is shown");
assert(shown.includes('href="agenthail://pair?endpoint=https%3A%2F%2Fserver.example.ts.net&amp;secret=s"'), "the link opens the app");

assert.equal((await reply(200, { id: "pairing-1", state: "pending" })).url, "/api/v1/pairings?id=pairing-1");
assert(node("#device-pairing").innerHTML.includes("Waiting for the iPhone"), "a pending code stays up");
await reply(200, { id: "pairing-1", state: "paired", device: { name: "New iPhone" } });
assert.equal((await reply(200, { devices: [phone, { ...phone, id: "device-3", name: "New iPhone" }] })).url, "/api/v1/devices");
assert.deepEqual(toasts, ["Paired New iPhone."]);
assert(node("#devices-list").innerHTML.includes("New iPhone"), "the paired phone is listed");
assert(node("#device-pairing").innerHTML.includes("data-device-pair"), "the code is gone once used");

context.startDevicePairing();
await reply(201, { id: "pairing-2", pairingURL: "agenthail://pair?x", expiresAt: "2026-10-22T10:05:00Z", qr: "data:image/png;base64,AAAA" });
await reply(200, { id: "pairing-2", state: "expired" });
assert(node("#device-pairing").innerHTML.includes("expired"), "an expired code says so");

context.startDevicePairing();
await reply(409, { error: { code: "remote_access_required", message: "Turn on remote access before pairing: run agenthail dashboard remote." } });
assert(node("#device-pairing").innerHTML.includes("agenthail dashboard remote"), "the reason a code cannot be made is shown");

context.removeDevice({ dataset: { deviceRemove: "device-1" }, disabled: false });
const removed = await reply(200, { ok: true });
assert.equal(removed.options.method, "DELETE");
assert.deepEqual(JSON.parse(removed.options.body), { id: "device-1" });
await reply(200, { devices: [] });
assert(node("#devices-list").innerHTML.includes("No iPhone is paired"), "the list empties");
console.log("ok dashboard devices");
