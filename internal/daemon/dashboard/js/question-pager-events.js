import { app } from "./state.js";
import { pagerFor, choose, type, next, goTo, move, decision, renderChips, renderPage, renderActions, sessionCard } from "./question-pager.js";

const camel = (attribute) => attribute.replace(/-([a-z])/g, (_, letter) => letter.toUpperCase());

function sessionRequest(id) {
  return (app.requests?.requests || []).find((request) => request.id === id);
}

export function bindQuestionPager(respond, { card = sessionCard, lookup = sessionRequest } = {}) {
  const selector = `form[data-${card.question}]`;
  const key = camel(card.question);
  const sending = new Set();
  const requestOf = (form) => lookup(form.dataset[key]);

  const refresh = (form, pager, { page = true } = {}) => {
    form.querySelector(".pager-chips").innerHTML = renderChips(pager);
    if (page) form.querySelector(".pager-page").innerHTML = renderPage(pager);
    form.querySelector(".request-actions").innerHTML = renderActions(pager, form.dataset[key], card.respond);
    if (page) form.focus({ preventScroll: true });
  };

  const send = async (form, pager, answers) => {
    const requestId = form.dataset[key];
    if (sending.has(requestId)) return;
    sending.add(requestId);
    form.querySelectorAll("button").forEach((button) => { button.disabled = true; });
    try {
      await respond(requestId, decision(pager, answers));
    } finally {
      sending.delete(requestId);
    }
  };

  document.addEventListener("click", async (event) => {
    const form = event.target.closest(selector);
    const request = form && requestOf(form);
    if (!request) return;
    const pager = pagerFor(request);
    const step = event.target.closest("[data-pager-step]");
    if (step) {
      goTo(pager, Number(step.dataset.pagerStep));
      refresh(form, pager);
      return;
    }
    const pick = event.target.closest("[data-pager-pick]");
    if (!pick) return;
    const answers = choose(pager, Number(pick.dataset.pagerPick));
    if (answers) await send(form, pager, answers);
    else refresh(form, pager);
  });
  document.addEventListener("input", (event) => {
    const note = event.target.matches("[data-pager-note]");
    if (!note && !event.target.matches("[data-pager-other]")) return;
    const form = event.target.closest(selector);
    const request = form && requestOf(form);
    if (!request) return;
    const pager = pagerFor(request);
    if (note) {
      pager.note = event.target.value;
      return;
    }
    type(pager, event.target.value);
    refresh(form, pager, { page: false });
  });
  document.addEventListener("submit", async (event) => {
    const form = event.target.closest(selector);
    if (!form) return;
    event.preventDefault();
    const request = requestOf(form);
    if (!request) return;
    const pager = pagerFor(request);
    const answers = next(pager);
    if (answers) await send(form, pager, answers);
    else refresh(form, pager);
  });
  document.addEventListener("keydown", (event) => {
    const form = event.target.closest?.(selector);
    if (!form || event.target.matches("input, textarea, select") || event.metaKey || event.ctrlKey || event.altKey || event.shiftKey) return;
    const request = requestOf(form);
    if (!request) return;
    const pager = pagerFor(request);
    if (event.key === "Enter" && event.target === form) {
      event.preventDefault();
      form.requestSubmit();
      return;
    }
    if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
      event.preventDefault();
      move(pager, event.key === "ArrowLeft" ? -1 : 1);
      refresh(form, pager);
      return;
    }
    const number = /^[1-9]$/.test(event.key) ? Number(event.key) : 0;
    const option = pager.questions[pager.step]?.options?.[number - 1];
    if (!option) return;
    event.preventDefault();
    form.querySelectorAll("[data-pager-pick]")[number - 1]?.click();
  });
}
