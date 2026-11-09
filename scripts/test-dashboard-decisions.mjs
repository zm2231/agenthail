import "./test-home.mjs";
import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";
import vm from "node:vm";

const elements = { panel: {}, list: {} };
const app = {
  state: {
    daemon: { host: { name: "laptop" }, hosts: [{ id: "server", name: "server", state: "connected" }, { id: "gone", name: "gone", state: "unreachable" }] },
    sessions: [{ id: "lead", name: "release-lead" }, { id: "worker", name: "builder" }, { id: "server::far", name: "far-agent" }],
    projects: [{ id: "web-app", displayName: "web app" }],
    attention: [{ id: 1, sessionId: "lead", target: "release-lead", queueId: 31, reason: "Held for a decision", requestedAction: "retry or cancel", createdAt: "2026-03-01T09:00:00Z" }],
    deliveryProblems: [],
    leadReplacements: [],
    queue: [{ id: 31, sessionId: "lead", message: "Ship <it> after the backup", status: "dead" }],
  },
  selected: null,
};
const context = vm.createContext({
  app,
  $: (selector) => (selector.endsWith("panel") ? elements.panel : elements.list),
  escape: (value) => String(value).replaceAll("<", "&lt;").replaceAll(">", "&gt;"),
  timeAgo: () => "just now",
  displayName: (session) => session.name,
  AbortController,
  setTimeout,
  clearTimeout,
});
await dashboardModules(["decisions", "inbox"], context);

const local = {
  decisions: [
    { group: "waiting", sessionId: "worker", projectId: "web-app", kind: "request", reason: "Needs your approval", since: "2026-03-01T09:09:00Z", answerable: true, requests: [{ id: "r1", kind: "approval", tool: "Bash", prompt: "Bash: make <release>" }] },
    { group: "holding", sessionId: "lead", projectId: "web-app", kind: "ask", reason: "Asks: which layout", since: "2026-03-01T09:05:00Z", answerable: true, holdsUp: ["worker"], requests: [{ id: "a1", kind: "ask", tool: "", title: "Which layout?" }] },
    { group: "stopped", sessionId: "lead", kind: "incoming", reason: "1 message needs a decision", since: "2026-03-01T09:00:00Z", answerable: false },
  ],
  leadHandling: [{ sessionId: "worker2", heldBy: "lead", role: "lead", reason: "Needs your approval" }],
};
const remote = { decisions: [{ group: "stopped", sessionId: "far", kind: "outgoing", reason: "1 message not delivered", since: "2026-03-01T08:00:00Z", answerable: false }], leadHandling: [] };
const asked = [];
context.fetch = async (url) => {
  asked.push(url);
  if (url === "/api/v1/decisions") return { ok: true, status: 200, json: async () => local };
  if (url === "/api/v1/decisions?host=server") return { ok: true, status: 200, json: async () => remote };
  throw new Error("unexpected " + url);
};
const plain = (value) => JSON.parse(JSON.stringify(value));
const inbox = plain(await vm.runInContext("loadDecisions(fetch)", context));
assert.deepEqual(asked.sort(), ["/api/v1/decisions", "/api/v1/decisions?host=server"], "only connected computers are asked, the local one without a host");
assert.deepEqual(inbox.decisions.map((item) => `${item.group}:${item.sessionId}`), ["stopped:server::far", "stopped:lead", "holding:lead", "waiting:worker"]);
assert.equal(inbox.decisions[0].hostId, "server");
assert.deepEqual(inbox.unreachable, ["gone"], "a paired computer that isn't connected is named without being asked");
assert.deepEqual(inbox.decisions[2].holdsUp, ["worker"], "local ids stay as the dashboard knows them");

context.fetch = async (url) => {
  if (url.includes("host=server")) return { ok: false, status: 404, json: async () => ({ error: { code: "unknown_route" } }) };
  throw new Error("refused");
};
const failing = plain(await vm.runInContext("loadDecisions(fetch)", context));
assert.deepEqual(failing.outdated, ["server"]);
assert.deepEqual(failing.unreachable, ["gone", "laptop"]);

app.decisions = inbox;
vm.runInContext("renderInbox()", context);
const html = elements.list.innerHTML;
assert.equal(elements.panel.hidden, false);
assert.ok(html.indexOf("Stopped on you") < html.indexOf("Holding up work") && html.indexOf("Holding up work") < html.indexOf("Waiting on you"));
assert.match(html, /data-retry="31"/, "a held delivery keeps its buttons in Stopped on you");
assert.match(html, /data-queued-text>Ship &lt;it&gt; after the backup</, "a stuck message shows what it says");
assert.ok(!html.includes("1 message needs a decision"), "a local held delivery is listed once, with its buttons, not again as a decision");
assert.match(html, /data-decision="server::far"/, "a remote delivery problem opens its session");
assert.match(html, /Holds up 1/);
assert.match(html, /data-open-decision="worker"[^>]*>Answer</, "answering opens the Inbox card on that decision");
assert.match(html, /data-session="server::far"[^>]*>Open</, "a decision that cannot be answered here opens its session");
assert.ok(html.includes("Bash: make &lt;release&gt;"), "request text is escaped");
assert.match(html, /web app · builder/);
assert.match(html, /Handled by a lead/);
assert.match(html, /release-lead is answering its prompt/);

app.decisions = failing;
app.state.attention = [];
vm.runInContext("renderInbox()", context);
assert.match(elements.list.innerHTML, /laptop isn't answering/);
assert.match(elements.list.innerHTML, /Update Agenthail on server/);

app.decisions = { decisions: [], leadHandling: [], unreachable: [], outdated: [] };
vm.runInContext("renderInbox()", context);
assert.equal(elements.panel.hidden, true, "an empty inbox hides");

let loads = 0;
app.decisionsKey = undefined;
app.state.catalogSeq = 1;
app.state.decisionsRev = "r1";
context.countingLoader = async () => { loads++; return inbox; };
await vm.runInContext("refreshDecisions(countingLoader)", context);
await vm.runInContext("refreshDecisions(countingLoader)", context);
assert.equal(loads, 1, "unchanged decisions do not ask again");
app.state.catalogSeq = 99;
await vm.runInContext("refreshDecisions(countingLoader)", context);
assert.equal(loads, 1, "a catalog change that moves no decision does not ask again");
app.state.decisionsRev = "r2";
await vm.runInContext("refreshDecisions(countingLoader)", context);
assert.equal(loads, 2, "a decisions change asks again, including a remote wait ending outside the loaded rows");

const stale = { decisions: [{ group: "waiting", sessionId: "stale-session", kind: "request", reason: "already answered elsewhere", answerable: true, requests: [{ id: "old", kind: "approval", tool: "Bash", prompt: "old prompt" }] }], leadHandling: [], unreachable: [], outdated: [] };
const fresh = { decisions: [], leadHandling: [], unreachable: [], outdated: [] };
const pending = [];
context.slowLoader = () => { loads++; return new Promise((resolve) => pending.push(resolve)); };
app.state.decisionsRev = "r3";
elements.list.innerHTML = "";
const first = vm.runInContext("refreshDecisions(slowLoader)", context);
app.state.decisionsRev = "r4";
await vm.runInContext("refreshDecisions(slowLoader)", context);
assert.equal(loads, 3, "a change during a load does not start a second load at once");
pending.shift()(stale);
await new Promise((resolve) => setTimeout(resolve, 0));
assert.ok(!elements.list.innerHTML.includes("old prompt"), "a load the decisions moved past is never shown");
assert.equal(loads, 4, "the change that arrived during the load is read once the load ends");
pending.shift()(fresh);
await first;
assert.ok(!elements.list.innerHTML.includes("old prompt"));
assert.equal(app.decisionsKey.endsWith("|r4"), true);
assert.equal(app.decisions.decisions.length, 0, "the newest load is the one kept");

app.state.decisionsRev = "r6";
const rendering = vm.runInContext("refreshDecisions(slowLoader)", context);
const sameStart = loads;
await vm.runInContext("refreshDecisions(slowLoader)", context);
pending.shift()(stale);
await rendering;
assert.equal(loads, sameStart, "a render asking again for the position being loaded starts no load");
assert.equal(app.decisions.decisions[0].sessionId, "stale-session", "and does not drop the load it asked for");
assert.equal(app.decisionsKey.endsWith("|r6"), true);

const busy = (n) => ({ decisions: [{ group: "waiting", sessionId: `busy-${n}`, kind: "request", reason: "r", answerable: false }], leadHandling: [], unreachable: [], outdated: [] });
const streamStart = loads;
app.state.decisionsRev = "r10";
const streaming = vm.runInContext("refreshDecisions(slowLoader)", context);
for (let n = 1; n <= 3; n++) {
  app.state.decisionsRev = `r1${n}`;
  await vm.runInContext("refreshDecisions(slowLoader)", context);
  pending.shift()(busy(n));
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(loads - streamStart, n + 1, "each change during a load costs one more load, not more");
}
assert.equal(app.decisions.decisions[0].sessionId, "busy-2", "a steady stream of changes still shows a recent load");
pending.shift()(busy(4));
await streaming;
assert.equal(app.decisions.decisions[0].sessionId, "busy-4");
assert.equal(app.decisionsKey.endsWith("|r13"), true, "the quiet decisions' load is kept as current");
console.log("dashboard decisions ok");
