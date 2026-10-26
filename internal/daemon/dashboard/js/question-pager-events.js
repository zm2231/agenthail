import { app } from "./state.js";
import { pagerFor, choose, type, next, goTo, move, renderChips, renderPage, renderActions } from "./question-pager.js";

function requestOf(form) {
  return (app.requests?.requests || []).find((request) => request.id === form.dataset.requestQuestion);
}

function refresh(form, pager, { page = true } = {}) {
  form.querySelector(".pager-chips").innerHTML = renderChips(pager);
  if (page) form.querySelector(".pager-page").innerHTML = renderPage(pager);
  form.querySelector(".request-actions").innerHTML = renderActions(pager, form.dataset.requestQuestion);
  if (page) form.focus({ preventScroll: true });
}

async function send(form, answers, respond) {
  form.querySelectorAll("button").forEach((button) => { button.disabled = true; });
  await respond(form.dataset.requestQuestion, { behavior: "allow", answers });
}

export function bindQuestionPager(respond) {
  document.addEventListener("click", async (event) => {
    const form = event.target.closest("form[data-request-question]");
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
    const answers = choose(pager, pick.dataset.pagerPick);
    if (answers) await send(form, answers, respond);
    else refresh(form, pager);
  });
  document.addEventListener("input", (event) => {
    if (!event.target.matches("[data-pager-other]")) return;
    const form = event.target.closest("form[data-request-question]");
    const request = form && requestOf(form);
    if (!request) return;
    const pager = pagerFor(request);
    type(pager, event.target.value);
    refresh(form, pager, { page: false });
  });
  document.addEventListener("submit", async (event) => {
    const form = event.target.closest("form[data-request-question]");
    if (!form) return;
    event.preventDefault();
    const request = requestOf(form);
    if (!request) return;
    const pager = pagerFor(request);
    const answers = next(pager);
    if (answers) await send(form, answers, respond);
    else refresh(form, pager);
  });
  document.addEventListener("keydown", (event) => {
    const form = event.target.closest?.("form[data-request-question]");
    if (!form || event.target.matches("input, textarea, select") || event.metaKey || event.ctrlKey || event.altKey) return;
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
