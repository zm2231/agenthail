import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { callStateLabels, readableInput, toolCallStates, toolChanges, toolLine, toolSummary } from "../internal/daemon/dashboard/js/tool-activity.js";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const vectors = JSON.parse(fs.readFileSync(path.join(root, "scripts/fixtures/tool-activity-vectors.json"), "utf8"));

for (const v of vectors.calls) {
  const call = { title: v.name, text: v.text };
  const label = `${v.name} ${JSON.stringify(v.text).slice(0, 60)}`;
  assert.deepEqual({ ...toolLine(call) }, { verb: v.verb, object: v.object, code: v.code }, `line of ${label}`);
  assert.equal(toolSummary(v.name, v.text), v.summary, `summary of ${label}`);
  assert.equal(readableInput(v.text), v.input, `input of ${label}`);
  assert.deepEqual(toolChanges(call).map(({ path, added, removed }) => ({ path, added, removed })), v.changes, `changes of ${label}`);
}

for (const v of vectors.states) {
  assert.deepEqual(Object.fromEntries(toolCallStates(v.items, v.active)), v.states, v.name);
}
assert.deepEqual(callStateLabels, vectors.labels);
console.log(`ok ${vectors.calls.length} calls, ${vectors.states.length} timelines`);
