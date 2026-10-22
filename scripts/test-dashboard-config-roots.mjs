import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";

const source = fs.readFileSync(new URL("../internal/daemon/dashboard/app.js", import.meta.url), "utf8");
const start = source.indexOf("function renderStartConfigRoots(");
const end = source.indexOf("function renderStartLaunchers(", start);
assert(start >= 0 && end > start);

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
vm.runInNewContext(source.slice(start, end), context);

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
assert(select.innerHTML.includes("work · signed in as work@example.com · out of usage until"), "a root's usage fills in when it arrives");
assert.equal(select.value, "/home/me/.work/.claude", "filling in usage keeps the chosen root");

context.app.startHost = "server";
context.app.startOptions = { claudeConfigRoots: roots() };
context.renderStartConfigRoots(context.app.startOptions.claudeConfigRoots);
pending[0].resolve(answer(exhausted));
await settle();
assert(!select.innerHTML.includes("out of usage"), "an answer for the host the form left is dropped");
console.log("ok dashboard config roots");
