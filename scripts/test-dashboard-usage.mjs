import assert from "node:assert/strict";
import { POLL_MS, render, start, text, usageStrip, visible } from "../internal/daemon/dashboard/js/usage.js";

const now = Date.parse("2026-10-23T14:00:00Z");
const meter = (label, percent, until, extra = {}) => ({ provider: "claude", kind: "session", label, percent, asOf: "2026-10-23T13:59:00Z", aged: false, until, resetsAt: until, ...extra });
const sets = [
  { host: "studio", hostId: "h1", local: true, provider: "claude", account: "work@example.com", root: "/a", primary: true, meters: [meter("Claude", 40, "2026-10-23T16:12:00Z")] },
  { host: "studio", hostId: "h1", local: true, provider: "codex", meters: [meter("Codex week", 19, "2026-10-26T16:00:00Z", { provider: "codex", kind: "weekly" })] },
  { host: "studio", hostId: "h1", local: true, provider: "claude", account: "team@example.com", root: "/b", meters: [meter("Claude", 12, "2026-10-23T16:00:00Z"), meter("Claude week", 91, "2026-10-26T16:00:00Z", { severity: "critical" })] },
  { host: "vps", hostId: "h2", local: false, provider: "claude", account: "ops@example.com", primary: true, meters: [meter("Claude", 5, "2026-10-23T13:30:00Z")] },
  { host: "old", hostId: "h3", local: false, meters: [], problem: "old runs an older Agenthail. Update it to see its usage." },
];

const strip = usageStrip(sets, now);
assert.deepEqual(strip.meters.map((m) => m.label), ["Claude", "Codex week"], "this host's primary account and Codex show in full");
assert.deepEqual(strip.others.map((o) => o.title), ["team"], "another account shows compact; a host whose meters all passed shows nothing");
assert.equal(strip.others[0].percent, 91);
assert.equal(strip.others[0].critical, true);
assert.ok(strip.others[0].spoken.startsWith("team@example.com: Claude usage 12 percent"), strip.others[0].spoken);
assert.equal(strip.problem, null, "a problem stays quiet while there are meters");
assert.deepEqual(usageStrip([{ ...sets[3], meters: sets[0].meters }], now).others.map((o) => o.title), ["ops · vps"], "another host's account names the host");
assert.equal(usageStrip([{ host: "studio", hostId: "h1", local: true, provider: "claude", primary: true, meters: [], problem: "claude.ai could not be asked" }], now).problem, "claude.ai could not be asked");
assert.equal(text(sets[0].meters[0], now), "40% · resets 2h 12m");
assert.equal(text(meter("Claude week", 62, null, { aged: true, asOf: "2026-10-23T10:40:00Z", resetsAt: null }), now), "62% · as of 3h ago");
assert.deepEqual(visible(sets[3].meters, now), [], "a meter hides once its until passes");

class Node {
  constructor(tag) {
    this.tagName = tag;
    this.children = [];
    this.attributes = {};
    this.hidden = false;
    this.textContent = "";
  }
  append(...nodes) { this.children.push(...nodes); }
  replaceChildren(...nodes) { this.children = nodes; }
  setAttribute(name, value) { this.attributes[name] = value; }
}
const line = new Node("span");
const document = { createElement: (tag) => new Node(tag), getElementById: (id) => (id === "usage-line" ? line : null) };
const texts = (node) => [node.textContent, ...node.children.flatMap(texts)].filter(Boolean);

render(document, line, strip, now);
assert.equal(line.hidden, false);
assert.deepEqual(line.children.map((c) => c.className), ["usage-meter", "usage-meter", "usage-other critical"]);
assert.deepEqual(texts(line.children[0]), ["Claude", "40%", "· resets 2h 12m"]);
assert.equal(line.children[0].children[1].tagName, "meter");
assert.equal(line.children[2].attributes["aria-label"], strip.others[0].spoken, "every item reads out its full text");
render(document, line, usageStrip([], now), now);
assert.equal(line.hidden, true, "nothing to show hides the line");

const intervals = [];
let answer = { status: 200, ok: true, json: async () => ({ sets }) };
const started = start({ document, fetch: async (path) => { assert.equal(path, "/api/v1/usage"); return answer; }, setInterval: (fn, ms) => intervals.push(ms), now: () => now });
await started.ready;
assert.deepEqual(intervals, [POLL_MS, 60_000]);
assert.equal(line.hidden, false);
answer = { status: 404, ok: false };
await started.load();
assert.equal(line.hidden, true, "an older daemon with no usage route shows nothing");
assert.equal(start({ document: { getElementById: () => null }, fetch, setInterval }), null, "a page without the line does nothing");
console.log("ok");
