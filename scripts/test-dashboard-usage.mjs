import assert from "node:assert/strict";
import { POLL_MS, accounts, activeSessions, byHost, render, renderPanel, shortLabel, start, text, visible } from "../internal/daemon/dashboard/js/usage.js";
import { dropOrder, fit } from "../internal/daemon/dashboard/js/usage-fit.js";

const now = Date.parse("2026-10-23T14:00:00Z");
const meter = (label, percent, until, extra = {}) => ({ provider: "claude", kind: "session", label, percent, asOf: "2026-10-23T13:59:00Z", aged: false, until, resetsAt: until, ...extra });
const noLogin = "Signed in with an API key; no usage reported.";
const sets = [
  { host: "studio", hostId: "h1", local: true, provider: "claude", name: "default", account: "work@example.com", root: "/a", primary: true, asOf: "2026-10-23T13:57:00Z", meters: [meter("Claude", 40, "2026-10-23T16:12:00Z")] },
  { host: "studio", hostId: "h1", local: true, provider: "codex", meters: [meter("Codex week", 19, "2026-10-26T16:00:00Z", { provider: "codex", kind: "weekly" })] },
  { host: "studio", hostId: "h1", local: true, provider: "claude", name: "team", account: "team@example.com", root: "/b", meters: [meter("Claude", 12, "2026-10-23T16:00:00Z"), meter("Claude week", 91, "2026-10-26T16:00:00Z", { kind: "weekly_all", severity: "critical" }), meter("Claude week, Fable", 40, "2026-10-26T16:00:00Z", { kind: "weekly_scoped", scope: "Fable" })] },
  { host: "vps", hostId: "h2", local: false, provider: "claude", name: "default", login: "api_key", root: "/srv/claude", primary: true, meters: [], problem: noLogin, problemDetail: "claude.ai reports usage only for a claude.ai login." },
  { host: "vps", hostId: "h2", local: false, provider: "codex", meters: [meter("Codex week", 5, "2026-10-23T13:30:00Z", { provider: "codex", kind: "weekly" })] },
  { host: "old", hostId: "h3", local: false, meters: [], problem: "old runs an older Agenthail. Update it to see its usage." },
];

const list = accounts(sets, now);
assert.deepEqual(list.map((a) => a.title), ["work", "studio · Codex", "team", "vps (API key)", "vps · Codex", "old"], "every account gets a chip; a host prefix only when a name repeats across hosts");
const sameLocal = accounts([
  { host: "studio", hostId: "h1", provider: "claude", name: "default", account: "work@company-a.example", root: "/a" },
  { host: "studio", hostId: "h1", provider: "claude", name: "second", account: "work@company-b.example", root: "/b" },
  { host: "vps", hostId: "h2", provider: "claude", name: "default", account: "work@company-c.example", root: "/c" },
  { host: "vps", hostId: "h2", provider: "claude", name: "x", login: "api_key", root: "/x" },
  { host: "vps", hostId: "h2", provider: "claude", name: "y", login: "api_key", root: "/y" },
], now).map((a) => a.title);
assert.deepEqual(sameLocal, ["studio · work · default", "studio · work · second", "vps · work", "vps (API key) · x", "vps (API key) · y"], "accounts one host would name alike are told apart by root");
assert.equal(new Set(sameLocal).size, sameLocal.length, "every chip title is distinct");
assert.equal(shortLabel(sets[2].meters[2]), "Week, Fable");
assert.equal(shortLabel({ kind: "weekly_scoped", label: "x" }), "Week, one model");
assert.equal(list[3].login, "API key");
assert.equal(list[3].problemDetail, "claude.ai reports usage only for a claude.ai login.");
assert.equal(list[0].subtitle, "default · read 3m ago");
assert.equal(list[2].worst.percent, 91);
assert.equal(list[2].critical, true);
assert.ok(list[2].spoken.startsWith("team@example.com: Claude usage 12 percent"), list[2].spoken);
assert.equal(list[3].problem, noLogin, "an API-key root keeps its problem");
assert.equal(list[3].fullTitle, "API key on vps");
assert.deepEqual(list[4].meters, [], "a reading past its window keeps the account with no meters");
assert.deepEqual(byHost(list).map((g) => [g.name, g.accounts.length]), [["studio", 3], ["vps", 2], ["old", 1]]);
assert.equal(text(sets[0].meters[0], now), "40% · resets 2h 12m");
assert.equal(text(meter("Claude week", 62, null, { aged: true, asOf: "2026-10-23T10:40:00Z", resetsAt: null }), now), "62% · as of 3h ago");
assert.deepEqual(visible(sets[4].meters, now), [], "a meter hides once its until passes");

const session = (id, surface, host, root, status, extra = {}) => ({ id, surface, host: { id: host }, claudeConfigRoot: root ? { root } : undefined, status, current: false, ...extra });
const sessions = [
  session("s1", "claude", "h1", "/a", "busy"),
  session("s2", "claude", "h1", "/b", "idle", { current: true }),
  session("s3", "claude", "h1", "/b", "idle"),
  session("s4", "claude", "h2", "/a", "busy"),
  session("s5", "codex", "h1", null, "busy"),
  session("s6", "claude", "h1", "/a", "busy", { archived: true }),
];
assert.deepEqual(activeSessions(list[0], sessions).map((s) => s.id), ["s1"], "a session belongs to its root and host");
assert.deepEqual(activeSessions(list[2], sessions).map((s) => s.id), ["s2"], "only working or current sessions are listed");
assert.deepEqual(activeSessions(list[1], sessions).map((s) => s.id), ["s5"]);

class Node {
  constructor(tag) {
    this.tagName = tag;
    this.children = [];
    this.attributes = {};
    this.listeners = {};
    this.hidden = false;
    this.textContent = "";
    this.className = "";
  }
  append(...nodes) { this.children.push(...nodes); }
  replaceChildren(...nodes) { this.children = nodes; }
  setAttribute(name, value) { this.attributes[name] = value; }
  removeAttribute(name) { delete this.attributes[name]; }
  addEventListener(name, fn) { this.listeners[name] = fn; }
  contains(node) { return node === this || this.children.some((child) => child.contains?.(node)); }
}
const line = new Node("button");
const panel = new Node("div");
panel.hidden = true;
const document = { createElement: (tag) => new Node(tag), getElementById: (id) => ({ "usage-line": line, "usage-panel": panel })[id] || null, addEventListener() {} };
const texts = (node) => [node.textContent, ...node.children.flatMap(texts)].filter(Boolean);

render(document, line, list);
assert.equal(line.hidden, false);
assert.deepEqual(line.children.map((c) => c.className), ["usage-chip", "usage-chip", "usage-chip critical", "usage-chip problem", "usage-chip", "usage-chip problem", "usage-more"]);
assert.deepEqual(texts(line.children[0]), ["work", "40%"]);
assert.equal(line.children[0].children[1].children[0].tagName, "meter");
assert.deepEqual(texts(line.children[3]), ["vps (API key)", "!"], "an account with only a problem still shows");
assert.equal(line.children[2].attributes["aria-label"], list[2].spoken, "every chip reads out its full text");
render(document, line, []);
assert.equal(line.hidden, true, "nothing to show hides the line");

const opened = [];
renderPanel(document, panel, list, sessions, now, (id) => opened.push(id));
const cards = panel.children.filter((c) => c.tagName === "section");
assert.equal(cards.length, list.length, "the panel has one card per account");
assert.deepEqual(panel.children.filter((c) => c.tagName === "h3").map((c) => c.textContent), ["studio", "vps", "old"], "cards group by host");
assert.ok(texts(cards[2]).includes("Week") && texts(cards[2]).includes("91%"), texts(cards[2]).join("|"));
assert.ok(texts(cards[3]).includes(noLogin));
const button = cards[0].children.find((c) => c.className === "usage-sessions").children[0];
button.listeners.click();
assert.deepEqual(opened, ["s1"], "a session in the panel opens it");

const item = (className) => ({ className, hidden: true, textContent: "" });
const attributed = (children) => ({ children, attributes: {}, setAttribute(name, value) { this.attributes[name] = value; }, removeAttribute(name) { delete this.attributes[name]; } });
const roomy = attributed([item("usage-chip"), item("usage-more")]);
fit(roomy, (l) => !("data-compact" in l.attributes));
assert.equal("data-compact" in roomy.attributes, true, "a line that overflows with bars first drops the bars");
assert.equal(roomy.children[0].hidden, false, "and keeps every account when that is enough");
fit(roomy, () => false);
assert.equal("data-compact" in roomy.attributes, false, "with room the bars come back");
const crowded = attributed([item("usage-chip"), item("usage-chip critical"), item("usage-chip problem"), item("usage-chip"), item("usage-chip"), item("usage-more")]);
const [calmFirst, criticalChip, problemChip, calmMid, calmLast, more] = crowded.children;
assert.deepEqual(dropOrder(crowded.children), [calmLast, calmMid, calmFirst], "calm accounts go from the end; critical and problem accounts never");
const room = (count) => (l) => l.children.filter((c) => !c.hidden).length > count;
fit(crowded, room(4));
assert.deepEqual(crowded.children.filter((c) => !c.hidden), [calmFirst, criticalChip, problemChip, more]);
assert.equal(more.textContent, "+2", "the line counts the accounts it dropped");
fit(crowded, room(6));
assert.equal(crowded.children.filter((c) => !c.hidden).length, 5, "with room nothing is dropped and the counter hides");
assert.equal(more.hidden, true);

const intervals = [];
let answer = { status: 200, ok: true, json: async () => ({ sets }) };
const started = start({ document, fetch: async (path) => { assert.equal(path, "/api/v1/usage"); return answer; }, setInterval: (fn, ms) => intervals.push(ms), now: () => now, sessions: () => sessions, open: (id) => opened.push(id) });
await started.ready;
assert.deepEqual(intervals, [POLL_MS, 60_000]);
assert.equal(line.hidden, false);
line.listeners.click();
assert.equal(panel.hidden, false, "clicking the line opens the panel");
assert.equal(line.attributes["aria-expanded"], "true");
assert.equal(panel.children.filter((c) => c.tagName === "section").length, list.length);
started.toggle(false);
assert.equal(panel.hidden, true);
answer = { status: 404, ok: false };
await started.load();
assert.equal(line.hidden, true, "an older daemon with no usage route shows nothing");
assert.equal(start({ document: { getElementById: () => null }, fetch, setInterval }), null, "a page without the line does nothing");
console.log("ok");
