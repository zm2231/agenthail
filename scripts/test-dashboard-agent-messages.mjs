import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { agentFoldLabel, agentFoldSenders, agentRuns, foldAgentEntries, renderAgentMessage, renderAgentSend } from "../internal/daemon/dashboard/js/agent-messages.js";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const vectors = JSON.parse(fs.readFileSync(path.join(root, "scripts/fixtures/agent-fold-vectors.json"), "utf8"));

for (const v of vectors.cases) {
  const runs = agentRuns(v.agents);
  assert.deepEqual(runs, v.runs, `runs of ${v.name}`);
  const groups = runs.map(([start, end]) => v.agents.slice(start, end));
  assert.deepEqual(groups.map(agentFoldLabel), v.labels, `labels of ${v.name}`);
  assert.deepEqual(groups.map(agentFoldSenders), v.senders, `senders of ${v.name}`);
}

const session = { id: "s1" };
const peer = { id: "p1", kind: "message", role: "peer", text: "Long **body**", files: [{ name: "a.txt", path: "/x/a.txt" }], agent: { direction: "in", kind: "peer", peer: "@builder", channel: "ops", summary: "Long body <b>" } };
const html = renderAgentMessage(peer, session, false);
assert.match(html, /^<details class="timeline-item agent-message"/);
assert.match(html, /data-files/);
assert.match(html, /@builder · #ops/);
assert.match(html, /Long body &lt;b&gt;/);
assert.doesNotMatch(html, / open>/);
assert.match(renderAgentMessage(peer, session, true), /data-locked open>/);

const send = { id: "c1", kind: "toolCall", callId: "k", agent: { direction: "out", kind: "send", peer: "@collab", summary: "Done", body: "Done\nmore" } };
assert.match(renderAgentSend(send, { status: "failed" }, session, false), /<details class="timeline-item agent-send".*“Done”.*@collab.*failed/s);
assert.match(renderAgentSend({ ...send, agent: { ...send.agent, body: "" } }, null, session, false), /^<p class="timeline-item agent-send"/);

const entries = [{ html: "a", agent: peer.agent }, { html: "b", agent: peer.agent }, { html: "reply" }, { html: "c", agent: peer.agent }];
const folded = foldAgentEntries(entries, session);
assert.equal(folded.length, 3);
assert.match(folded[0], /agent-fold.*2 messages from @builder · #ops.*ab<\/div>/s);
assert.deepEqual(folded.slice(1), ["reply", "c"]);

console.log(`ok ${vectors.cases.length} fold vectors, agent rows render`);
