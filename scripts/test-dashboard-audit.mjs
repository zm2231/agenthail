import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";

const source = fs.readFileSync(new URL("../internal/daemon/dashboard/app.js", import.meta.url), "utf8");
const start = source.indexOf("function auditKindLabel");
const end = source.indexOf("function signinOffHint", start);
assert(start >= 0 && end > start);
const context = {};
vm.runInNewContext(source.slice(start, end) + "\nthis.label = auditKindLabel;", context);

assert.equal(context.label("peer_send"), "Peer send");
assert.equal(context.label("transport-accepted"), "Transport accepted");
assert.equal(context.label(""), "Event");
console.log("dashboard audit tests passed");
