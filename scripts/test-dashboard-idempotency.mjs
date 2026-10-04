import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import vm from "node:vm";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const source = fs.readFileSync(path.join(root, "internal/daemon/dashboard/app.js"), "utf8");
const start = source.indexOf("function isNetworkFailure");
const end = source.indexOf("async function voiceRequest");
assert(start >= 0 && end > start, "logical action source boundary missing");

const calls = [];
const responses = [];
const context = {
  app: { selected: { id: "session-1" }, pendingIdempotency: new Map() },
  crypto: { randomUUID: (() => { let n = 0; return () => `key-${++n}`; })() },
  fetch: async (_url, request) => {
    calls.push({ key: request.headers["Idempotency-Key"], body: JSON.parse(request.body) });
    const next = responses.shift();
    if (next instanceof Error) throw next;
    return next;
  },
};
vm.runInNewContext(`${source.slice(start, end)}\nglobalThis.runLogicalAction = logicalAction;\nglobalThis.deliveryStatusLabel = deliveryStatusLabel;`, context);

const ok = (body = { ok: true }) => ({ ok: true, json: async () => body });
const httpFailure = () => ({ ok: false, status: 502, text: async () => "typed failure" });
const networkLoss = () => new TypeError("network lost");

assert.equal(context.deliveryStatusLabel({ ok: true, status: "submitted" }), "Submitted", "pending 202 top-level status must remain Submitted");
assert.equal(context.deliveryStatusLabel({ ok: true, result: { evidence: "transport_accepted" } }), "Sent", "transport acceptance is Sent");

responses.push(networkLoss(), ok());
await assert.rejects(() => context.runLogicalAction("send", "send", { message: "one", effort: "high" }));
await context.runLogicalAction("send", "send", { message: "one", effort: "high" });
assert.equal(calls[0].key, calls[1].key, "unchanged send retry must reuse its key");

responses.push(networkLoss(), ok());
await assert.rejects(() => context.runLogicalAction("send", "send", { message: "one", effort: "high" }));
await context.runLogicalAction("send", "send", { message: "one", effort: "low" });
assert.notEqual(calls[2].key, calls[3].key, "edited send options must rotate its key");

responses.push(ok(), ok());
await context.runLogicalAction("send", "send", { message: "one", effort: "low" });
await context.runLogicalAction("send", "send", { message: "one", effort: "low" });
assert.notEqual(calls[4].key, calls[5].key, "successful send must clear its key");

responses.push({ ok: true, json: async () => { throw new SyntaxError("lost response body"); } }, ok());
await assert.rejects(() => context.runLogicalAction("send", "send", { message: "two" }));
await context.runLogicalAction("send", "send", { message: "two" });
assert.equal(calls[6].key, calls[7].key, "lost successful response body must retain its key");

responses.push(httpFailure(), ok());
await assert.rejects(() => context.runLogicalAction("send", "send", { message: "three" }));
await context.runLogicalAction("send", "send", { message: "three" });
assert.notEqual(calls[8].key, calls[9].key, "typed HTTP failure must clear its key");

context.app.selected = { id: "session-2" };
responses.push(networkLoss(), ok());
await assert.rejects(() => context.runLogicalAction("session-create", "session-create", { surface: "codex", message: "create" }));
context.app.selected = { id: "session-3" };
await context.runLogicalAction("session-create", "session-create", { surface: "codex", message: "create" });
assert.notEqual(calls[10].key, calls[11].key, "create fingerprint must include its serialized session context");

assert.equal(calls[10].body.action, "session-create");
assert.equal(calls[10].body.sessionId, "session-2");
assert.equal(calls[11].body.sessionId, "session-3");
