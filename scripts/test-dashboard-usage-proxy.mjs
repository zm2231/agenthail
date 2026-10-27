import assert from "node:assert/strict";
import { accounts, activeSessions, byProxy, render, renderPanel } from "../internal/daemon/dashboard/js/usage.js";
import { strip } from "../internal/daemon/dashboard/js/usage-strip.js";
import { rootTitles } from "../internal/daemon/dashboard/js/start-root-labels.js";

const now = Date.parse("2026-10-26T12:00:00Z");
const meter = (kind, label, percent, extra = {}) => ({ provider: "claude", kind, label, percent, asOf: "2026-10-26T11:48:00Z", resetsAt: "2026-10-26T15:00:00Z", until: "2026-10-26T15:00:00Z", ...extra });
const shared = { name: "shared", id: "http://proxy.example:8317", url: "http://proxy.example:8317" };
const sets = [
  { host: "laptop", hostId: "h1", local: true, provider: "claude", name: "default", account: "dev@example.com", root: "/a", primary: true, meters: [meter("session", "Claude", 30)] },
  { host: "laptop", hostId: "h1", local: true, provider: "claude", name: "work", login: "proxy", root: "/w", proxy: shared, account: "stale@example.com", meters: [] },
  { host: "server", hostId: "h2", local: false, provider: "claude", name: "default", login: "proxy", root: "/srv", proxy: shared, meters: [] },
  { host: "server", hostId: "h2", local: false, provider: "proxy", name: "shared", proxy: shared, meters: [], problem: "Every shared Claude account is over its ceiling; the first frees up in 2h 20m.", accounts: [
    { id: "a", provider: "claude", label: "dev@example.com", status: "active", asOf: "2026-10-26T11:48:00Z", meters: [meter("session", "Claude", 18, { ceiling: 60 }), meter("weekly", "Claude week", 61, { ceiling: 60, severity: "critical" })] },
    { id: "b", provider: "claude", label: "team@example.com", status: "cooling", nextRetry: "2026-10-26T14:20:00Z", meters: [] },
    { id: "c", provider: "codex", label: "dev@example.com", status: "active", meters: [] },
  ] },
  { host: "server", hostId: "h2", local: false, provider: "proxy", name: "local", proxy: { name: "local", id: "http://loopback:8317@h2" }, meters: [], problem: "No management key for the local proxy on this host; its accounts are not readable here.", proxyProblem: "key_missing" },
];

const list = accounts(sets, now);
assert.deepEqual(list.map((a) => a.title), ["dev", "dev · shared", "team", "dev (Codex)", "local (proxy)"], "a proxy account is named like any other, told apart by its proxy, never by the host that read it");
assert.equal(list.length, 5, "a root through a proxy is not an account of its own; each proxy account is");
assert.ok(!list.some((a) => a.root === "/w" || a.root === "/srv"));
const dev = list.find((a) => a.group?.name === "shared" && a.provider === "claude" && a.login === "dev@example.com");
assert.equal(dev.critical, true, "an account at its proxy ceiling is critical");
assert.equal(dev.fullTitle, "dev@example.com via the shared proxy");
const team = list.find((a) => a.login === "team@example.com");
assert.equal(team.problem, "cooling for 2h 20m");
assert.equal(list.find((a) => a.group?.name === "shared" && a.provider === "codex").login, "dev@example.com (Codex)");
const local = list.find((a) => a.provider === "proxy");
assert.equal(local.title, "local (proxy)");
assert.equal(local.problem, sets[4].problem, "a proxy whose accounts cannot be read still shows, with why");

const session = (id, host, root, status) => ({ id, surface: "claude", host: { id: host }, claudeConfigRoot: { root }, status, current: false });
const sessions = [session("s1", "h1", "/w", "busy"), session("s2", "h2", "/srv", "busy"), session("s3", "h1", "/a", "busy")];
assert.deepEqual(activeSessions(dev, sessions).map((s) => s.id), ["s1", "s2"], "a proxy's sessions are every session on a root through it, on any host");

const groups = byProxy(list);
assert.deepEqual(groups.map((g) => [g.title, g.accounts.length, g.roots.length]), [["shared (proxy)", 3, 2], ["local (proxy)", 1, 0]]);

class Node {
  constructor(tag) { this.tagName = tag; this.children = []; this.attributes = {}; this.listeners = {}; this.textContent = ""; this.className = ""; }
  append(...nodes) { this.children.push(...nodes); }
  replaceChildren(...nodes) { this.children = nodes; }
  setAttribute(name, value) { this.attributes[name] = value; }
  addEventListener(name, fn) { this.listeners[name] = fn; }
}
const document = { createElement: (tag) => new Node(tag) };
const texts = (node) => [node.textContent, ...node.children.flatMap(texts)].filter(Boolean);
const panel = new Node("div");
renderPanel(document, panel, list, sessions, now, () => {});
const heads = panel.children.filter((c) => c.tagName === "h3").map((c) => texts(c).join(""));
assert.deepEqual(heads, ["laptop", "shared (proxy) read from server", "local (proxy) read from server"], "each proxy is its own group, after the hosts");
const all = texts(panel);
assert.equal(dev.subtitle, "cap 60% · over cap · read 12m ago", "an account's one proxy ceiling shows once, and says when a window is over it");
assert.ok(all.includes("cap 60% · over cap · read 12m ago"));
const chips = strip(list);
assert.deepEqual(chips.map((a) => a.title), ["dev", "shared · dev", "local (proxy)"], "each proxy is one chip named for it and its fullest account");
assert.deepEqual(chips[1].meters.map((m) => m.percent), [61]);
assert.equal(chips[1].critical, true, "the proxy chip carries its worst window, in its tone");
assert.ok(chips[1].spoken.includes("team@example.com via the shared proxy: cooling for 2h 20m"), "the chip still reads out every account");
const line = new Node("div");
render(document, line, chips);
assert.equal(line.children.filter((c) => c.className.startsWith("usage-chip")).length, 3);
assert.ok(chips[2].meters.length === 0 && chips[2].problem, "an unreadable proxy's chip still shows why");
const mixed = accounts([{ ...sets[3], accounts: [{ ...sets[3].accounts[0], meters: [meter("session", "Claude", 18, { ceiling: 60 }), meter("weekly", "Claude week", 41, { ceiling: 80 })] }] }], now)[0];
assert.equal(mixed.capsDiffer, true);
const mixedPanel = new Node("div");
renderPanel(document, mixedPanel, [mixed], [], now, () => {});
assert.ok(texts(mixedPanel).includes("Week, cap 80%"), "windows with different ceilings each name theirs");
assert.ok(all.includes(sets[3].problem), "the group's problem shows under its accounts");
const lists = panel.children.filter((c) => c.className === "usage-sessions");
assert.equal(lists.length, 1, "the proxy group has one session list, not one per account");
assert.deepEqual(texts(lists[0]), ["s1", "s2"]);

assert.deepEqual(rootTitles([{ name: "default", primary: true, signedInAs: { email: "dev@example.com" } }, { name: "work", login: "proxy", proxy: "shared", signedInAs: { email: "stale@example.com" } }], "laptop"), ["dev", "shared (proxy)"], "a root through a proxy is named for the proxy");
console.log("ok");
