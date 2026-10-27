import assert from "node:assert/strict";
import { accounts, renderPanel } from "../internal/daemon/dashboard/js/usage.js";
import { openPrefer, planQuery, planSections, preferenceWord } from "../internal/daemon/dashboard/js/usage-prefer.js";

const now = Date.parse("2026-10-26T12:00:00Z");
const shared = { name: "shared", id: "http://proxy.example:8317", url: "http://proxy.example:8317" };
const sets = [
  { host: "server", hostId: "h2", local: false, provider: "proxy", name: "shared", proxy: shared, meters: [], accounts: [
    { id: "claude-ops.json", provider: "claude", label: "ops@example.com", status: "active", preference: 10, preferred: true, setOn: "server", setAt: "2026-10-26T11:20:00Z", meters: [] },
    { id: "claude-spare.json", provider: "claude", label: "spare@example.com", status: "active", preference: 0, meters: [] },
    { id: "codex-ops.json", provider: "codex", label: "ops@example.com", status: "active", preference: 0, meters: [] },
  ] },
];

class Node {
  constructor(tag) { this.tagName = tag; this.children = []; this.attributes = {}; this.listeners = {}; this.textContent = ""; this.className = ""; this.hidden = false; this.disabled = false; this.checked = false; }
  append(...nodes) { this.children.push(...nodes); }
  replaceChildren(...nodes) { this.children = nodes; }
  setAttribute(name, value) { this.attributes[name] = value; }
  addEventListener(name, fn) { this.listeners[name] = fn; }
  showModal() { this.open = true; }
  close() { this.open = false; }
}
const ids = {};
const document = { createElement: (tag) => new Node(tag), getElementById: (id) => (ids[id] ||= new Node(id)) };
const texts = (node) => [node.textContent, ...node.children.flatMap(texts)].filter(Boolean);
const find = (node, test) => (test(node) ? node : node.children.map((child) => find(child, test)).find(Boolean));

const list = accounts(sets, now);
const ops = list.find((account) => account.login === "ops@example.com");
assert.match(ops.subtitle, /^preferred/, "a preferred account says so, not only by color");
assert.deepEqual(ops.proxyAccount, { proxyId: shared.id, id: "claude-ops.json", preferred: true, setOn: "server", setAt: "2026-10-26T11:20:00Z" });

const panel = new Node("div");
const asked = [];
renderPanel(document, panel, list, [], now, () => {}, (account, set) => asked.push([account.login, set]));
const toggles = [];
const collect = (node) => {
  if (node.className?.startsWith("usage-prefer-toggle")) toggles.push(node);
  node.children.forEach(collect);
};
collect(panel);
assert.equal(toggles.length, 3, "every proxy account row has the toggle");
const onlies = [];
const collectOnly = (node) => {
  if (node.className === "usage-prefer-only") onlies.push(node);
  node.children.forEach(collectOnly);
};
collectOnly(panel);
assert.equal(onlies.length, 2, "prefer only is offered where the provider has another account to turn off");
assert.equal(toggles[0].attributes["aria-pressed"], "true");
toggles[0].listeners.click();
toggles[1].listeners.click();
find(panel, (node) => node.className === "usage-prefer-only").listeners.click();
assert.deepEqual(asked, [["ops@example.com", "off"], ["spare@example.com", "on"], ["ops@example.com", "only"]], "the toggle flips one account; prefer only is its own action");
assert.ok(texts(panel).some((text) => text.startsWith("set on server at ")), "a value Agenthail wrote is credited");

const withoutPrefer = new Node("div");
renderPanel(document, withoutPrefer, list, [], now, () => {});
assert.ok(!find(withoutPrefer, (node) => node.className === "usage-prefer"), "no action without a handler");

assert.equal(planQuery(ops.proxyAccount, "only", true), "proxy=http%3A%2F%2Fproxy.example%3A8317&account=claude-ops.json&set=only&now=true");
const plan = {
  set: "only", now: true, planId: "p1", confirm: "Prefer only spare@example.com (turns off ops@example.com) and move up to 1 running session",
  changes: [{ id: "claude-spare.json", label: "spare@example.com", before: 0, after: 10 }, { id: "claude-ops.json", label: "ops@example.com", before: 10, after: 0 }],
  release: [{ id: "claude-ops.json", label: "ops@example.com" }],
  hosts: [{ host: "laptop", roots: ["/home/dev/.work/.claude"], sessions: [{ id: "s6", name: "migrator" }] }, { host: "server", roots: ["/srv/claude"], sessions: [] }],
  warnings: ["The proxy does not say which account each session is on."],
};
assert.deepEqual(planSections(plan), {
  changes: [
    { text: "spare@example.com: not preferred → preferred", help: "quota_preference 0 → 10" },
    { text: "ops@example.com: preferred → not preferred", help: "quota_preference 10 → 0" },
    { text: "Releases the sessions on ops@example.com, so they move on their next request." },
  ],
  affects: ["laptop: /home/dev/.work/.claude · running: migrator", "server: /srv/claude · no running sessions"],
  notes: ["The proxy does not say which account each session is on."],
}, "plain words, the raw value only in the help text, then what it affects");
assert.deepEqual(planSections({ ...plan, set: "on", changes: plan.changes.slice(0, 1), release: [], hosts: [], warnings: [] }).changes.map((change) => change.text), ["spare@example.com: not preferred → preferred", "Other accounts are not changed."]);
assert.deepEqual(planSections({ nothing: "ops@example.com is already preferred." }).changes, [{ text: "ops@example.com is already preferred." }]);
assert.equal(preferenceWord(5), "preference 5", "a value Agenthail did not set is shown as it is");

const spare = list.find((account) => account.login === "spare@example.com");
const calls = [];
let stale = true;
const respond = (status, body) => ({ ok: status < 300, status, json: async () => body });
const fetch = async (url, options = {}) => {
  calls.push([options.method || "GET", url, options.body ? JSON.parse(options.body) : null]);
  if (options.method !== "POST") return respond(200, plan);
  if (stale) {
    stale = false;
    return respond(409, { error: { code: "plan_stale", message: "The proxy's preferences changed since you confirmed; review again." } });
  }
  return respond(200, { done: ["spare@example.com set to 10"], message: "spare@example.com set to 10." });
};
const notes = [];
let reloads = 0;
await openPrefer({ document, fetch, account: spare, set: "only", notify: (text) => notes.push(text), reload: () => reloads++ });
assert.equal(ids["prefer-dialog"].open, true);
assert.equal(ids["prefer-now-row"].hidden, false, "prefer only offers moving running sessions");
assert.equal(ids["prefer-now"].checked, false, "moving sessions costs a prompt-cache rebuild, so it starts off");
assert.equal(calls.at(-1)[1], `/api/v1/proxies/prefer?${planQuery(spare.proxyAccount, "only", false)}`);
assert.equal(ids["prefer-confirm"].textContent, plan.confirm, "the button is named for the effect");
assert.deepEqual(ids["prefer-plan"].children.map((item) => item.className), ["", "", "", "prefer-plan-heading", "", "", "prefer-plan-note"], "the account lines, then Affects with the hosts, then the notes");
assert.equal(ids["prefer-plan"].children[0].title, "quota_preference 0 → 10");
ids["prefer-cancel"].onclick();
assert.equal(ids["prefer-dialog"].open, false, "Cancel closes without writing");
ids["prefer-dialog"].showModal();
ids["prefer-now"].checked = true;
await ids["prefer-now"].onchange();
assert.equal(calls.at(-1)[1], `/api/v1/proxies/prefer?${planQuery(spare.proxyAccount, "only", true)}`, "moving sessions re-reads the plan");
await ids["prefer-form"].onsubmit({ preventDefault() {} });
assert.equal(ids["prefer-error"].hidden, false, "a stale plan is refused and shown");
assert.equal(calls.at(-1)[0], "GET", "and the plan is read again");
await ids["prefer-form"].onsubmit({ preventDefault() {} });
assert.deepEqual(calls.at(-1), ["POST", "/api/v1/proxies/prefer", { proxy: shared.id, account: "claude-spare.json", set: "only", now: true, planId: "p1" }]);
assert.equal(ids["prefer-dialog"].open, false);
assert.deepEqual(notes, ["spare@example.com set to 10."]);
assert.equal(reloads, 1);

await openPrefer({ document, fetch: async () => respond(409, { error: { code: "proxy_unreadable", message: "No management key for the shared proxy on this host." } }), account: spare, set: "on", notify: () => {}, reload: () => {} });
assert.equal(ids["prefer-now-row"].hidden, true);
assert.equal(ids["prefer-confirm"].disabled, true, "nothing can be confirmed without a plan");
assert.equal(ids["prefer-error"].textContent, "No management key for the shared proxy on this host.");
console.log("dashboard usage prefer ok");
