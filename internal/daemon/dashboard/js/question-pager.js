import { escape } from "./format.js";

const draftLimit = 16;
const drafts = new Map();

export function createPager(questions) {
  return { questions, step: 0, picks: questions.map(() => []), typed: questions.map(() => "") };
}

export function pagerFor(request) {
  const questions = request.questions || [];
  const saved = drafts.get(request.id);
  if (saved && JSON.stringify(saved.questions) === JSON.stringify(questions)) return saved;
  const pager = createPager(questions);
  savePager(request.id, pager);
  return pager;
}

export function savePager(requestId, pager) {
  drafts.delete(requestId);
  drafts.set(requestId, pager);
  while (drafts.size > draftLimit) drafts.delete(drafts.keys().next().value);
}

export const isOneTap = (pager) => pager.questions.length === 1 && !pager.questions[0].multiSelect;
export const isSingle = (pager) => pager.questions.length === 1;
export const reviewStep = (pager) => pager.questions.length;
export const onReview = (pager) => !isSingle(pager) && pager.step === reviewStep(pager);

export function answerAt(pager, index) {
  const question = pager.questions[index];
  if (!question) return null;
  const other = (pager.typed[index] || "").trim();
  if (other) return other;
  const picked = (question.options || []).map((option) => option.label).filter((label) => pager.picks[index].includes(label));
  return picked.length ? picked.join(", ") : null;
}

export const isAnswered = (pager, index) => answerAt(pager, index) !== null;
export const canSubmit = (pager) => pager.questions.every((_, index) => isAnswered(pager, index));

export function answers(pager) {
  if (!canSubmit(pager)) return null;
  return Object.fromEntries(pager.questions.map((question, index) => [question.question, answerAt(pager, index)]));
}

export function choose(pager, label) {
  const question = pager.questions[pager.step];
  if (!question) return null;
  if (isOneTap(pager)) return { [question.question]: label };
  const picked = pager.picks[pager.step];
  if (question.multiSelect) {
    pager.picks[pager.step] = picked.includes(label) ? picked.filter((value) => value !== label) : [...picked, label];
    return null;
  }
  pager.picks[pager.step] = [label];
  pager.typed[pager.step] = "";
  pager.step += 1;
  return null;
}

export function type(pager, text) {
  if (pager.step < pager.questions.length) pager.typed[pager.step] = text;
}

export function next(pager) {
  if (isSingle(pager) || onReview(pager)) return answers(pager);
  pager.step = Math.min(pager.step + 1, reviewStep(pager));
  return null;
}

export function goTo(pager, step) {
  if (!isSingle(pager)) pager.step = Math.max(0, Math.min(step, reviewStep(pager)));
}

export const move = (pager, offset) => goTo(pager, pager.step + offset);

const title = (question, index) => question.header || `Question ${index + 1}`;

export function renderChips(pager) {
  if (isSingle(pager)) return "";
  const chip = (label, step, mark, state) => `<button type="button" class="pager-chip" data-pager-step="${step}"${pager.step === step ? ' aria-current="step"' : ""}${state ? ` aria-label="${escape(`${label}, ${state}`)}"` : ""}><span aria-hidden="true">${mark}</span><span class="pager-chip-label">${escape(label)}</span></button>`;
  const questions = pager.questions.map((question, index) => {
    const answered = isAnswered(pager, index);
    return chip(title(question, index), index, answered ? "☑" : "☐", answered ? "answered" : "not answered");
  });
  return [...questions, chip("Submit", reviewStep(pager), "➤", "")].join("");
}

export function renderPage(pager) {
  if (onReview(pager)) {
    const rows = pager.questions.map((question, index) => {
      const answer = answerAt(pager, index);
      return `<button type="button" class="pager-review-row${answer === null ? " unanswered" : ""}" data-pager-step="${index}"><small>${escape(question.header || question.question)}</small><span>${answer === null ? "Not answered" : escape(answer)}</span></button>`;
    }).join("");
    return `<p class="pager-question">Review your answers</p>${rows}${canSubmit(pager) ? "" : "<p>Answer every question to submit.</p>"}`;
  }
  const question = pager.questions[pager.step];
  if (!question) return "";
  const header = isSingle(pager) && question.header ? `<small>${escape(question.header)}</small>` : "";
  const options = (question.options || []).map((option, index) => {
    const picked = pager.picks[pager.step].includes(option.label);
    return `<button type="button" class="pager-option" data-pager-pick="${escape(option.label)}" role="${question.multiSelect ? "checkbox" : "radio"}" aria-checked="${picked}"><span aria-hidden="true">${question.multiSelect ? (picked ? "☑" : "☐") : (picked ? "◉" : "○")}</span><span><span>${escape(option.label)}</span>${option.description ? `<small>${escape(option.description)}</small>` : ""}</span>${index < 9 ? `<kbd>${index + 1}</kbd>` : ""}</button>`;
  }).join("");
  return `${header}<p class="pager-question">${escape(question.question)}</p>${options}<label><span>Other</span><input type="text" data-pager-other placeholder="Your own answer" value="${escape(pager.typed[pager.step] || "")}"></label>`;
}

export function renderActions(pager, requestId) {
  const decline = `<button class="soft-button" type="button" data-request-respond="deny" data-request-id="${escape(requestId)}">Decline</button>`;
  let primary = "";
  if (onReview(pager) || (isSingle(pager) && !isOneTap(pager))) {
    primary = `<button class="primary-button" type="submit"${canSubmit(pager) ? "" : " disabled"}>${isSingle(pager) ? "Submit" : "Submit answers"}</button>`;
  } else if (isOneTap(pager)) {
    primary = canSubmit(pager) ? '<button class="primary-button" type="submit">Answer</button>' : "";
  } else {
    primary = '<button class="soft-button" type="submit">Next</button>';
  }
  return primary + decline;
}

export function renderQuestionCard(request, created) {
  const pager = pagerFor(request);
  return `<form class="request-card question-pager" tabindex="-1" data-request-question="${escape(request.id)}"><h3>Claude asks${created}</h3><div class="pager-chips" aria-label="Questions">${renderChips(pager)}</div><div class="pager-page">${renderPage(pager)}</div><div class="request-actions">${renderActions(pager, request.id)}</div></form>`;
}
