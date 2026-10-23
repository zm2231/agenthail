import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { alignmentSteps, callStateLabels, diffEntries, outputLines, outputPreview, readableInput, textDiff, toolDiffs, toolCallStates, toolChanges, toolLine, toolSummary } from "../internal/daemon/dashboard/js/tool-activity.js";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const vectors = JSON.parse(fs.readFileSync(path.join(root, "scripts/fixtures/tool-activity-vectors.json"), "utf8"));

for (const v of vectors.calls) {
  const call = { title: v.name, text: v.text };
  const label = `${v.name} ${JSON.stringify(v.text).slice(0, 60)}`;
  assert.deepEqual({ ...toolLine(call) }, { verb: v.verb, object: v.object, code: v.code }, `line of ${label}`);
  assert.equal(toolSummary(v.name, v.text), v.summary, `summary of ${label}`);
  assert.equal(readableInput(v.text), v.input, `input of ${label}`);
  assert.deepEqual(toolChanges(call).map(({ path, added, removed }) => ({ path, added, removed })), v.changes, `changes of ${label}`);
  assert.deepEqual(toolDiffs(call).map((diff) => ({ path: diff.path, rows: diffEntries(diff.hunks) })), v.diffs, `diffs of ${label}`);
}

for (const v of vectors.states) {
  assert.deepEqual(Object.fromEntries(toolCallStates(v.items, v.active)), v.states, v.name);
}
assert.deepEqual(callStateLabels, vectors.labels);
for (const v of vectors.alignments) {
  assert.deepEqual(alignmentSteps(v.old, v.new, v.limit), v.steps, `alignment of ${JSON.stringify(v.old)} and ${JSON.stringify(v.new)} within ${v.limit}`);
}
for (const v of vectors.diffs) {
  assert.deepEqual(diffEntries([textDiff(v.before, v.after)]), v.rows, `diff of ${JSON.stringify(v.before).slice(0, 40)} to ${JSON.stringify(v.after).slice(0, 40)}`);
}
for (const v of vectors.outputs) {
  const label = `output ${JSON.stringify(v.text).slice(0, 40)}`;
  assert.equal(outputPreview(v.text), v.preview, `preview of ${label}`);
  assert.equal(outputLines(v.text).length, v.lines, `lines of ${label}`);
}
console.log(`ok ${vectors.calls.length} calls, ${vectors.states.length} timelines, ${vectors.outputs.length} outputs, ${vectors.alignments.length} alignments, ${vectors.diffs.length} diffs`);
