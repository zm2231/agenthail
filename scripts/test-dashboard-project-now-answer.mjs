import "./test-home.mjs";
import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";
import vm from "node:vm";

const waiting = (id) => ({ id, name: id, surface: "claude", status: "idle", waitingOn: { kind: "approval", since: "2026-11-22T10:00:00Z" } });
const bash = { id: "req-bash", kind: "approval", tool: "Bash", prompt: "Bash", summary: "go test ./...", detail: "go test ./...\n(in /work/fieldnotes)" };
const deploy = { id: "req-deploy", kind: "approval", tool: "mcp__ops__deploy", prompt: "mcp__ops__deploy", detail: '{"env":"prod","force":true}' };
const color = { id: "req-color", kind: "question", tool: "AskUserQuestion", questions: [{ question: "Which index?", header: "", multiSelect: false, options: [{ id: "o0", label: "Full text" }, { id: "o1", label: "Prefix" }], suggested: 0 }] };
const decisions = [
  { group: "waiting", sessionId: "tests", answerable: true, reason: "Needs approval", requests: [bash] },
  { group: "waiting", sessionId: "deploy", answerable: true, reason: "Needs approval", requests: [deploy] },
  { group: "waiting", sessionId: "index", answerable: true, reason: "Asks", requests: [color] },
  { group: "waiting", sessionId: "busy", answerable: true, reason: "Needs approval", requests: [bash, { ...deploy, id: "req-2" }] },
  { group: "waiting", sessionId: "noallow", answerable: true, reason: "Needs approval", requests: [{ ...bash, id: "req-noallow", noAllow: true }] },
  { group: "waiting", sessionId: "remote", answerable: false, reason: "On a computer that isn't reachable" },
];
const context = vm.createContext({
  app: { state: { sessions: [], projects: [] }, decisions: { decisions, leadHandling: [], unreachable: [], outdated: [] } },
  crypto: { randomUUID: () => "key" },
});
await dashboardModules(["project-now-answer"], context);
const run = (code) => vm.runInContext(code, context);
const offer = (id) => run("inlineAnswer")(waiting(id));
const html = (id) => run("renderInlineAnswer")(waiting(id));

assert.equal(offer("tests").kind, "allow", "an approval that fits one line is allowed in place");
assert.equal(offer("noallow").kind, "answer", "an approval with no allow is opened, not allowed in place");
assert.match(html("tests"), /<span class="project-now-tool">Bash<\/span><code>go test \.\/\.\.\.<\/code>/, "Allow says the tool and the command it runs");
assert.match(html("tests"), /<details class="project-now-detail"><summary>Details<\/summary><pre>go test \.\/\.\.\.\n\(in \/work\/fieldnotes\)<\/pre><\/details>/, "the full request is one tap away");
assert.match(html("tests"), /data-now-respond="allow"[^>]*data-request-id="req-bash"[^>]*>Allow<\/button>.*data-now-respond="deny"[^>]*>Deny<\/button>/s);

assert.equal(offer("deploy").kind, "answer", "an approval with no one-line summary is never allowed blind");
assert.doesNotMatch(html("deploy"), /Allow/);
assert.match(html("deploy"), /<span class="project-now-tool">mcp__ops__deploy<\/span><span>Needs approval<\/span>.*data-open-decision="deploy"[^>]*>Answer<\/button>/s, "Answer says what the row waits on");

assert.equal(offer("index").kind, "recommended");
assert.match(html("index"), /Which index\?.*data-now-accept[^>]*>Full text<\/button>.*>Answer<\/button>/s, "a single question offers its recommended option and the full question");
assert.equal(offer("busy").kind, "answer", "a row with several open requests opens them all");
assert.equal(offer("remote"), null, "a request no one here can answer gets no button");
assert.equal(run("inlineAnswer")({ id: "tests", status: "busy" }), null, "a row that isn't waiting gets nothing");

const target = (data) => ({ dataset: data });
assert.deepEqual(JSON.parse(JSON.stringify(run("nowDecision")(target({ sessionId: "tests", requestId: "req-bash", nowRespond: "deny" })))), { choices: ["deny"] });
assert.deepEqual(JSON.parse(JSON.stringify(run("nowDecision")(target({ sessionId: "index", requestId: "req-color", nowAccept: "" })))), { choices: ["o0"] });
assert.equal(run("nowDecision")(target({ sessionId: "tests", requestId: "gone", nowRespond: "allow" })), null, "a request that left the feed is not answered");

const posts = [];
assert.equal(await run("answerInPlace")("tests", "req-bash", { choices: ["allow"] }, async (wire) => posts.push(wire)), true);
assert.deepEqual(JSON.parse(JSON.stringify(posts)), [{ action: "respond", sessionId: "tests", requestId: "req-bash", decision: { choices: ["allow"] } }]);
assert.equal(offer("tests"), null, "an answered request leaves the row");

assert.equal(await run("answerInPlace")("index", "req-color", { choices: ["o0"] }, async () => { throw new Error("request was already answered or has expired"); }), true);
assert.equal(offer("index"), null, "a request answered elsewhere leaves the row too");
assert.match(html("deploy"), /Answer/);
assert.equal(await run("answerInPlace")("deploy", "req-deploy", { choices: ["allow"] }, async () => { throw new Error("the session is not reachable"); }), false);
assert.match(run("renderInlineAnswer")(waiting("deploy")), /Answer/, "a failed answer keeps the request");
assert.equal(context.app.nowAnswerFailure.message, "the session is not reachable");

console.log("dashboard project now answers ok");
