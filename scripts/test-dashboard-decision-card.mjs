import "./test-home.mjs";
import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";
import vm from "node:vm";

const listeners = {};
const elements = {};
const element = (id) => (elements[id] ||= { innerHTML: "", hidden: false });
const posts = [];
const ask = { id: "turn-end-1", kind: "ask", tool: "", title: "Ready to ship", questions: [{ question: "Deploy it now?", header: "", multiSelect: false, options: [{ label: "Deploy" }, { label: "Wait" }], suggested: 0 }] };
const app = {
  state: { sessions: [{ id: "S", name: "builder" }, { id: "T", name: "writer" }], projects: [] },
  decisions: {
    decisions: [
      { group: "waiting", sessionId: "S", kind: "ask", reason: "Ready to ship", answerable: true, requests: [ask] },
      { group: "waiting", sessionId: "T", kind: "request", reason: "Needs approval", answerable: true, requests: [{ id: "turn-end-1", kind: "approval", tool: "Bash" }] },
    ],
    leadHandling: [], unreachable: [], outdated: [],
  },
  scope: "inbox",
};
const context = vm.createContext({
  app,
  $: (selector) => element(selector),
  document: { addEventListener: (type, handler) => { (listeners[type] ||= []).push(handler); }, querySelector: () => null },
  escape: (value) => String(value).replaceAll("<", "&lt;").replaceAll(">", "&gt;"),
  timeAgo: () => "just now",
  displayName: (session) => session.name,
  toast: () => {},
  postAction: async (wire) => { posts.push(wire); return { ok: true }; },
  crypto: { randomUUID: () => "key" },
  sessionStorage: { getItem: () => null, setItem: () => {}, removeItem: () => {} },
  localStorage: { getItem: () => null, setItem: () => {}, removeItem: () => {} },
});
await dashboardModules(["decision-card", "pane-scope"], context);
const run = (name) => vm.runInContext(name, context);

const recommended = run("recommended");
assert.deepEqual(JSON.parse(JSON.stringify(recommended(ask))), { behavior: "allow", answers: { "Deploy it now?": "Deploy" } }, "every question's suggestion is the recommended answer");
assert.equal(recommended({ ...ask, questions: [{ ...ask.questions[0], suggested: undefined }] }), null, "a question without a suggestion offers none");
assert.equal(recommended({ ...ask, questions: [{ ...ask.questions[0], suggested: 7 }] }), null, "a suggestion naming no option offers none");
assert.equal(recommended({ id: "p", kind: "approval", tool: "Bash" }), null, "an approval has no recommended answer");

const accepts = run("acceptsRecommended");
assert.equal(accepts({ key: "Enter", shiftKey: false }), false, "a bare Enter never accepts");
assert.equal(accepts({ key: "Enter", shiftKey: true }), true, "Shift+Enter accepts");
assert.equal(accepts({ key: "Enter", shiftKey: true, metaKey: true }), false, "other modified Enters do not");

const bindDecisionCard = run("bindDecisionCard");
let reloads = 0;
bindDecisionCard(async () => { reloads++; });
const key = async (event) => { for (const handler of listeners.keydown || []) await handler({ preventDefault() {}, target: { matches: () => false, closest: () => null }, ...event }); };
await key({ key: "Enter", shiftKey: false });
assert.equal(posts.length, 0, "a bare Enter with nothing picked sends nothing");
await key({ key: "Enter", shiftKey: true });
assert.equal(posts.length, 1, "Shift+Enter sends exactly one answer");
assert.equal(JSON.stringify(posts[0]), JSON.stringify({ action: "respond", sessionId: "S", requestId: "turn-end-1", decision: { behavior: "allow", answers: { "Deploy it now?": "Deploy" } } }), "it answers the selected session's request with the recommendation");
assert.equal(reloads, 1, "answering asks for the decisions again");
const shown = run("shownDecisions")().map((item) => item.sessionId);
assert.deepEqual(shown, ["T"], "the answered ask leaves the Inbox while the same request id on another session stays");
assert.equal(run("selectedDecision")().sessionId, "T", "the next decision is selected");
await key({ key: "Enter", shiftKey: true });
assert.equal(posts.length, 1, "an approval has nothing to accept with Shift+Enter");
assert.ok(element("#session-list").innerHTML.includes('data-decision-select="T"'), "the Inbox list shows what is left");

const scopeKeeps = run("scopeKeeps");
const after = run("scopeAfterSelecting");
assert.ok(scopeKeeps("all", "p") && scopeKeeps("p", "p") && !scopeKeeps("p", "q") && !scopeKeeps("p", undefined), "a project scope keeps only its sessions");
assert.equal(after("inbox", { project: { id: "p" } }), "all", "opening a session leaves the Inbox");
assert.equal(after("p", { project: { id: "p" } }), "p", "opening one of the project's sessions keeps its scope");
assert.equal(after("p", { project: { id: "q" } }), "all", "a session from another project leaves the scope");
console.log("dashboard decision card ok");
