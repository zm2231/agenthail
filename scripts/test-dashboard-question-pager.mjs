import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";

const context = {
  escape: (value) => String(value).replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`),
};
await dashboardModules(["question-pager"], context);
const pagers = context;
const same = (actual, expected, message) => assert.deepEqual(JSON.parse(JSON.stringify(actual)), expected, message);

const scope = { question: "Which scope?", header: "Scope", multiSelect: false, options: [{ label: "Yes", description: "" }, { label: "No", description: "Leave it" }] };
const targets = { question: "Which targets?", header: "Targets", multiSelect: true, options: [{ label: "Mac", description: "" }, { label: "iPhone", description: "" }, { label: "Web", description: "" }] };

const oneTap = pagers.createPager([scope]);
assert.ok(pagers.isOneTap(oneTap) && pagers.isSingle(oneTap) && !pagers.onReview(oneTap));
same(pagers.choose(oneTap, "No"), { "Which scope?": "No" });
pagers.type(oneTap, "  Maybe  ");
same(pagers.next(oneTap), { "Which scope?": "Maybe" });
assert.equal(pagers.renderChips(oneTap), "", "a single question has no chips");
assert.doesNotMatch(pagers.renderActions(oneTap, "r"), /Next|Submit/);

const single = pagers.createPager([targets]);
assert.equal(pagers.next(single), null, "an unanswered single question does not send");
assert.equal(pagers.choose(single, "Web"), null);
assert.equal(pagers.choose(single, "Mac"), null);
same(pagers.next(single), { "Which targets?": "Mac, Web" }, "multi-select answers keep option order");
assert.match(pagers.renderActions(single, "r"), />Submit</);

const pager = pagers.createPager([scope, targets]);
assert.equal(pagers.reviewStep(pager), 2);
assert.equal(pagers.choose(pager, "Yes"), null);
assert.equal(pager.step, 1, "a single-select pick advances");
assert.ok(pagers.isAnswered(pager, 0) && !pagers.isAnswered(pager, 1) && !pagers.canSubmit(pager));
pagers.choose(pager, "iPhone");
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
assert.ok(pager.picks[0].includes("Yes"), "going back keeps the answer");
pagers.choose(pager, "No");
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
pagers.choose(draft, "Yes");
assert.equal(pagers.pagerFor(request).step, 1, "a re-render keeps the step for the same request");
assert.equal(pagers.pagerFor({ id: "q2", kind: "question", questions: [scope, targets] }).step, 0, "another request starts at the first page");
const card = pagers.renderQuestionCard(request, "");
assert.match(card, /data-request-question="q1"/);
assert.match(card, /Which targets\?/);
assert.match(card, /<kbd>1<\/kbd>/);

const hostile = pagers.renderQuestionCard({ id: "q3", kind: "question", questions: [{ question: "<b>x</b>", header: "<i>", multiSelect: false, options: [{ label: "<img>", description: "" }] }, scope] }, "");
assert.doesNotMatch(hostile, /<b>|<i>|<img>/);
console.log("dashboard question pager ok");
