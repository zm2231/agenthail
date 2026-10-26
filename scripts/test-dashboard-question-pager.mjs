import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";

const listeners = {};
const context = {
  escape: (value) => String(value).replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`),
  document: { addEventListener: (type, listener) => (listeners[type] ||= []).push(listener) },
};
await dashboardModules(["question-pager", "question-pager-events", "state"], context);
const pagers = context;
const same = (actual, expected, message) => assert.deepEqual(JSON.parse(JSON.stringify(actual)), expected, message);

const scope = { question: "Which scope?", header: "Scope", multiSelect: false, options: [{ label: "Yes", description: "" }, { label: "No", description: "Leave it" }] };
const targets = { question: "Which targets?", header: "Targets", multiSelect: true, options: [{ label: "Mac", description: "" }, { label: "iPhone", description: "" }, { label: "Web", description: "" }] };

const oneTap = pagers.createPager([scope]);
assert.ok(pagers.isOneTap(oneTap) && pagers.isSingle(oneTap) && !pagers.onReview(oneTap));
same(pagers.choose(oneTap, 1), { "Which scope?": "No" });
assert.equal(pagers.choose(oneTap, 7), null, "a missing option picks nothing");
pagers.type(oneTap, "  Maybe  ");
same(pagers.next(oneTap), { "Which scope?": "Maybe" });
assert.equal(pagers.renderChips(oneTap), "", "a single question has no chips");
assert.doesNotMatch(pagers.renderActions(oneTap, "r"), /Next|Submit/);

const single = pagers.createPager([targets]);
assert.equal(pagers.next(single), null, "an unanswered single question does not send");
assert.equal(pagers.choose(single, 2), null);
assert.equal(pagers.choose(single, 0), null);
same(pagers.next(single), { "Which targets?": "Mac, Web" }, "multi-select answers keep option order");
assert.match(pagers.renderActions(single, "r"), />Submit</);

const twins = pagers.createPager([{ question: "Which copies?", header: "", multiSelect: true, options: [{ label: "same", description: "first" }, { label: "same", description: "second" }] }]);
pagers.choose(twins, 0);
pagers.choose(twins, 1);
same(pagers.next(twins), { "Which copies?": "same, same" }, "options with the same label are picked apart");
pagers.choose(twins, 0);
same(pagers.answers(twins), { "Which copies?": "same" }, "unpicking one leaves its twin");

const pager = pagers.createPager([scope, targets]);
assert.equal(pagers.reviewStep(pager), 2);
assert.equal(pagers.choose(pager, 0), null);
assert.equal(pager.step, 1, "a single-select pick advances");
assert.ok(pagers.isAnswered(pager, 0) && !pagers.isAnswered(pager, 1) && !pagers.canSubmit(pager));
pagers.choose(pager, 1);
assert.equal(pager.step, 1, "a multi-select toggle stays on its page");
assert.match(pagers.renderActions(pager, "r"), />Next</);
assert.match(pagers.renderActions(pager, "r"), /data-request-respond="deny"/, "decline is on every page");
assert.equal(pagers.next(pager), null);
assert.ok(pagers.onReview(pager));
const review = pagers.renderPage(pager);
assert.match(review, /Scope<\/small><span>Yes/);
assert.match(review, /Targets<\/small><span>iPhone/);
assert.match(pagers.renderActions(pager, "r"), /Submit answers/);
assert.doesNotMatch(pagers.renderActions(pager, "r"), /disabled/);
pagers.goTo(pager, 0);
assert.ok(pager.picks[0].includes(0), "going back keeps the answer");
pagers.choose(pager, 1);
assert.equal(pagers.answerAt(pager, 0), "No");
pagers.move(pager, 9);
assert.ok(pagers.onReview(pager), "moving clamps at review");
pagers.move(pager, -9);
assert.equal(pager.step, 0, "moving clamps at the first page");
pagers.type(pager, "Both");
assert.equal(pagers.answerAt(pager, 0), "Both", "a typed answer counts without next");
pagers.type(pager, "");
assert.equal(pagers.answerAt(pager, 0), "No");
pagers.goTo(pager, 2);
same(pagers.next(pager), { "Which scope?": "No", "Which targets?": "iPhone" });

const gated = pagers.createPager([scope, targets]);
pagers.goTo(gated, 2);
assert.equal(pagers.next(gated), null, "review does not send while a question is unanswered");
assert.match(pagers.renderPage(gated), /unanswered/);
assert.match(pagers.renderPage(gated), /Answer every question to submit/);
assert.match(pagers.renderActions(gated, "r"), /Submit answers[^]*disabled|disabled[^]*Submit answers/);
const chips = pagers.renderChips(gated);
assert.match(chips, /data-pager-step="2" aria-current="step"/);
assert.match(chips, /Scope, not answered/);

const request = { id: "q1", kind: "question", questions: [scope, targets] };
const draft = pagers.pagerFor(request);
pagers.choose(draft, 0);
assert.equal(pagers.pagerFor(request).step, 1, "a re-render keeps the step for the same request");
assert.equal(pagers.pagerFor({ id: "q2", kind: "question", questions: [scope, targets] }).step, 0, "another request starts at the first page");
assert.equal(pagers.pagerFor({ id: "q1", kind: "question", questions: [targets] }).step, 0, "a draft for other questions is not reused");
const card = pagers.renderQuestionCard({ id: "q4", kind: "question", questions: [targets, scope] }, "");
assert.match(card, /data-request-question="q4"/);
assert.match(card, /<kbd aria-hidden="true">1<\/kbd><span aria-hidden="true">☐<\/span><span><span>Mac/, "the key hint sits before its option");
assert.match(card, /data-pager-pick="2"/, "options are picked by position");

const hostile = pagers.renderQuestionCard({ id: "q3", kind: "question", questions: [{ question: "<b>x</b>", header: "<i>", multiSelect: false, options: [{ label: "<img>", description: "" }] }, scope] }, "");
assert.doesNotMatch(hostile, /<b>|<i>|<img>/);

const sent = [];
let release = () => {};
pagers.bindQuestionPager((requestId, decision) => {
  sent.push({ requestId, decision });
  return new Promise((resolve) => { release = resolve; });
});
const slot = () => ({ innerHTML: "" });
const parts = { ".pager-chips": slot(), ".pager-page": slot(), ".request-actions": slot() };
let submits = 0;
const form = {
  dataset: { requestQuestion: "dom" },
  querySelector: (selector) => parts[selector],
  querySelectorAll: (selector) => (selector === "[data-pager-pick]" ? [0, 1, 2].map(pick) : []),
  focus() {},
  closest: (selector) => (selector.startsWith("form") ? form : null),
  matches: () => false,
  requestSubmit() {
    submits++;
    return fire("submit", { target: form });
  },
};
function element(dataset, { other = false } = {}) {
  const node = {
    dataset,
    value: "",
    closest: (selector) => (selector.startsWith("form") ? form : (selector === "[data-pager-step]" && "pagerStep" in dataset) || (selector === "[data-pager-pick]" && "pagerPick" in dataset) ? node : null),
    matches: (selector) => (selector === "[data-pager-other]" || selector === "input, textarea, select" ? other : false),
    click: () => fire("click", { target: node }),
  };
  return node;
}
const pick = (index) => element({ pagerPick: String(index) });
function fire(type, event) {
  return Promise.all((listeners[type] || []).map((listener) => listener({ preventDefault() {}, ...event })));
}
const key = (key, modifiers = {}) => fire("keydown", { target: form, key, ...modifiers });

context.app.requests = { requests: [{ id: "dom", kind: "question", questions: [scope, targets] }] };
const live = () => pagers.pagerFor(context.app.requests.requests[0]);
await fire("click", { target: pick(0) });
assert.equal(live().step, 1, "a click on an option advances");
assert.match(parts[".pager-page"].innerHTML, /Which targets\?/, "the page re-renders on the next question");
await key("2");
assert.ok(live().picks[1].includes(1), "a number key picks its option");
await key("Enter", { shiftKey: true });
await key("ArrowRight", { shiftKey: true });
await key("ArrowLeft", { metaKey: true });
assert.equal(submits, 0, "a modified Enter does not submit");
assert.equal(live().step, 1, "modified arrows do not move");
const other = element({}, { other: true });
other.value = "Everywhere";
await fire("keydown", { target: other, key: "1" });
assert.ok(!live().picks[1].includes(0), "keys typed into the Other field do not pick");
const page = parts[".pager-page"].innerHTML;
await fire("input", { target: other });
assert.equal(pagers.answerAt(live(), 1), "Everywhere", "typing counts as an answer");
assert.equal(parts[".pager-page"].innerHTML, page, "typing leaves the page, and so the field's focus, in place");
assert.match(parts[".pager-chips"].innerHTML, /Targets, answered/, "the chip marks the typed answer at once");
await key("ArrowRight");
assert.ok(pagers.onReview(live()), "an arrow moves to the review");
const first = key("Enter");
const second = key("Enter");
assert.equal(submits, 2);
same(sent, [{ requestId: "dom", decision: { behavior: "allow", answers: { "Which scope?": "Yes", "Which targets?": "Everywhere" } } }], "a second Enter while the answer is in flight sends nothing");
release();
await Promise.all([first, second]);
await key("Enter");
assert.equal(sent.length, 2, "once the first answer settles a retry can send");
release();
await fire("click", { target: element({ pagerStep: "0" }) });
assert.equal(live().step, 0, "a chip goes back");
console.log("dashboard question pager ok");
