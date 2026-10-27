import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";

const select = { closest: () => ({ dataset: {} }) };
const pending = [];
const context = {
  app: { startHost: "", startOptions: null },
  escape: (value) => String(value ?? "").replaceAll("<", "&lt;").replaceAll(">", "&gt;").replaceAll('"', "&quot;"),
  $: () => select,
  syncStartForm: () => {},
  startHostQuery: (prefix) => (context.app.startHost ? `${prefix}host=${context.app.startHost}` : ""),
  fetch: (url) => new Promise((resolve) => pending.push({ url, resolve })),
};
// Like a browser select: replacing the options selects the first one, and a
// value is kept only when an option has it.
const firstOption = (html) => html.match(/value="([^"]*)"/)?.[1] ?? "";
Object.defineProperty(select, "innerHTML", {
  get() { return this._html ?? ""; },
  set(next) { this._html = next; this._value = firstOption(next); },
});
Object.defineProperty(select, "value", {
  get() { return this._value ?? ""; },
  set(next) { this._value = this.innerHTML.includes(`value="${next}"`) ? next : firstOption(this.innerHTML); },
});
await dashboardModules(["start-roots"], context);

const roots = () => [
  { name: "default", root: "/home/me/.claude", primary: true, signedInAs: { email: "me@example.com" } },
  { name: "work", root: "/home/me/.work/.claude", primary: false, signedInAs: { email: "work@example.com" } },
];
const answer = (usage) => ({ ok: true, json: async () => ({ usage }) });
const settle = () => new Promise((resolve) => setTimeout(resolve, 0));
const exhausted = { limits: [{ kind: "session", percent: 100, resetsAt: new Date(Date.now() + 3600e3).toISOString() }] };

const options = { claudeConfigRoots: roots() };
context.app.startOptions = options;
context.renderStartConfigRoots(options.claudeConfigRoots);
assert(!select.innerHTML.includes("out of usage"), "roots render before any usage arrives");
select.value = "/home/me/.work/.claude";
context.loadStartRootUsage(options, "");
assert.deepEqual(pending.map((call) => call.url), [
  "/api/v1/session-options/claude-usage?root=%2Fhome%2Fme%2F.claude",
  "/api/v1/session-options/claude-usage?root=%2Fhome%2Fme%2F.work%2F.claude",
], "each signed-in root is asked once, at once");
pending[1].resolve(answer(exhausted));
await settle();
assert(select.innerHTML.includes(">work · out of usage until"), "a root's usage fills in when it arrives, named by its login");
assert.equal(select.value, "/home/me/.work/.claude", "filling in usage keeps the chosen root");

assert.equal(firstOption(select.innerHTML), "", "Automatic is the first choice and sends no root");
assert(select.innerHTML.includes('value="/home/me/.claude">me<'), "the primary root is chosen by its path, not by leaving the root out");
const before = pending.length;
context.loadStartRootPick(options, "");
assert.deepEqual(pending.slice(before).map((call) => call.url), ["/api/v1/session-options/claude-pick"]);
options.claudeConfigRoots[0].usage = { limits: [{ kind: "session", percent: 95 }, { kind: "weekly_all", percent: 40 }] };
pending[before].resolve({ ok: true, json: async () => ({ pick: { root: "/home/me/.work/.claude", name: "work", account: "work@example.com", reason: "most headroom, session 10%" }, policy: { sessionCeiling: 90, weeklyCeiling: 90 } }) });
await settle();
assert(select.innerHTML.includes(">Automatic (work, most headroom)</option>"), select.innerHTML);
assert(select.innerHTML.includes(">me · session 95%, week 40%, over limit</option>"), "each account shows its usage and whether it is over a ceiling");
assert.equal(select.textContent, "", "a root chosen by hand shows no automatic reason");
select.value = "";
context.syncStartRootDetail();
assert.equal(select.textContent, "Automatic picks work: most headroom, session 10%", "the reason shows under the picker, not cut off inside it");
select.value = "/home/me/.work/.claude";
assert.equal(select.value, "/home/me/.work/.claude", "the preview keeps the chosen root");
context.loadStartRootPick({ claudeConfigRoots: roots().slice(0, 1) }, "");
assert.equal(pending.length, before + 1, "one root needs no preview");

context.app.startHost = "server";
context.app.startOptions = { claudeConfigRoots: roots() };
context.renderStartConfigRoots(context.app.startOptions.claudeConfigRoots);
pending[0].resolve(answer(exhausted));
await settle();
assert(!select.innerHTML.includes("out of usage"), "an answer for the host the form left is dropped");

context.app.startHost = "";
const proxied = { claudeConfigRoots: [roots()[0], { name: "shared", root: "/home/me/.shared/.claude", primary: false, login: "proxy", proxy: "shared" }] };
context.app.startOptions = proxied;
const asked = pending.length;
context.loadStartRootUsage(proxied, "");
assert.equal(pending[asked + 1]?.url, "/api/v1/session-options/claude-usage?root=%2Fhome%2Fme%2F.shared%2F.claude", "a root through a proxy is asked too, though it holds no login");
pending[asked + 1].resolve({ ok: true, json: async () => ({ best: "team@example.com", usage: { limits: [{ kind: "session", percent: 18 }, { kind: "weekly_all", percent: 41 }] } }) });
await settle();
assert(select.innerHTML.includes(">shared via proxy · next: team · session 18%, week 41%</option>"), select.innerHTML);
console.log("ok dashboard config roots");
