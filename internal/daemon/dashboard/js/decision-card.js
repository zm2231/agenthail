import { app } from "./state.js";
import { $ } from "./dom.js";
import { escape, timeAgo } from "./format.js";
import { displayName } from "./session-labels.js";
import { postAction } from "./actions.js";
import { toast } from "./feedback.js";
import { decisionGroups } from "./decisions.js";
import { inboxStops } from "./inbox-stops.js";
import { renderQuestionCard } from "./question-pager.js";
import { bindQuestionPager } from "./question-pager-events.js";

export const decisionCard = { question: "decision-question", respond: "decision-respond" };
const mark = (sessionId, requestId) => `${sessionId}\u0001${requestId}`;

export function recommended(request) {
  if (!request || !["question", "ask"].includes(request.kind) || !request.questions?.length) return null;
  const choices = [];
  for (const question of request.questions) {
    const option = Number.isInteger(question.suggested) ? question.options?.[question.suggested] : null;
    if (!option?.id) return null;
    choices.push(option.id);
  }
  return { choices };
}

export function acceptsRecommended(event) {
  return event.key === "Enter" && event.shiftKey && !event.metaKey && !event.ctrlKey && !event.altKey;
}

export function shownDecisions() {
  const decisions = app.decisions?.decisions || [];
  const answered = app.decisionsAnswered || new Set();
  return decisions.flatMap((item) => {
    if (!item.requests?.length) return [item];
    const requests = item.requests.filter((request) => !answered.has(mark(item.sessionId, request.id)));
    return requests.length ? [{ ...item, requests }] : [];
  });
}

export function currentStops() {
  return inboxStops({ replacements: app.state.leadReplacements || [], decisions: shownDecisions() });
}

export function selectedDecision() {
  const decisions = shownDecisions();
  return decisions.find((item) => item.sessionId === app.decisionSelected) || decisions[0] || null;
}

function nameOf(id) {
  const session = (app.state.sessions || []).find((item) => item.id === id);
  return session ? displayName(session) : id;
}

function summary(item) {
  const requests = item.requests || [];
  if (requests.length < 2) return item.reason;
  return requests.every((request) => request.kind === "question" || request.kind === "ask") ? `${requests.length} questions` : `${requests.length} requests`;
}

export function renderDecisionList() {
  const decisions = shownDecisions();
  const selected = selectedDecision();
  const row = (item) => `<button class="session${item.sessionId === selected?.sessionId ? " active" : ""}" type="button" data-decision-select="${escape(item.sessionId)}"><div class="session-name"><span>${escape(nameOf(item.sessionId))}</span></div><div class="session-detail"><span>${escape(summary(item))}</span>${item.holdsUp?.length ? `<span class="decision-holds">Holds up ${item.holdsUp.length}</span>` : ""}</div></button>`;
  const groups = decisionGroups.map(([key, title]) => {
    const items = decisions.filter((item) => item.group === key);
    return items.length ? `<div class="filter-summary">${escape(title)} · ${items.length}</div>${items.map(row).join("")}` : "";
  }).join("");
  const inbox = app.decisions || { leadHandling: [], unreachable: [], outdated: [] };
  const leads = (inbox.leadHandling || []).map((item) => `<button class="session" type="button" data-session="${escape(item.sessionId)}"><div class="session-name"><span>${escape(nameOf(item.sessionId))}</span></div><div class="session-detail"><span>${escape(nameOf(item.heldBy))} has it · ${escape(item.reason)}</span></div></button>`).join("");
  const notes = [
    ...(inbox.unreachable || []).map((name) => `<p class="decision-note">${escape(name)} isn't answering. Its decisions aren't shown.</p>`),
    ...(inbox.outdated || []).map((name) => `<p class="decision-note">Update Agenthail on ${escape(name)} to see its decisions here.</p>`),
  ].join("");
  const body = groups + (leads ? `<div class="filter-summary">Handled by a lead</div>${leads}` : "") + notes;
  return body || `<div class="empty-state compact"><p>${app.decisions ? "Nothing needs you." : "Looking for decisions."}</p></div>`;
}

function renderApproval(item, request) {
  const title = request.title || `Claude wants to use ${request.tool}`;
  const input = request.input && Object.keys(request.input).length ? `<pre>${escape(JSON.stringify(request.input, null, 2))}</pre>` : "";
  const detail = [request.description, request.reason, request.blockedPath && `Path: ${request.blockedPath}`].filter(Boolean).map((line) => `<p>${escape(line)}</p>`).join("");
  const button = (choice, label, kind) => `<button class="${kind}" type="button" data-decision-respond="${choice}" data-request-id="${escape(request.id)}">${label}</button>`;
  return `<div class="request-card"><h3>${escape(title)}</h3>${detail}${input}<div class="request-actions">${button("allow", "Allow", "primary-button")}${request.alwaysAllow ? button("always", "Always allow", "soft-button") : ""}${button("deny", "Deny", "soft-button")}</div></div>`;
}

function renderRequest(item, request) {
  if (request.kind === "question") return renderQuestionCard(request, "", "Claude asks", decisionCard);
  if (request.kind === "ask") return renderQuestionCard(request, "", `${nameOf(item.sessionId)} asks: ${request.title || ""}`, decisionCard);
  if (request.kind === "input") return `<div class="request-card"><h3>${escape(request.title || "An MCP server asks for input")}</h3><p>Open the session to fill in this form.</p></div>`;
  return renderApproval(item, request);
}

export function renderDecisionPane() {
  const pane = $("#decision-pane");
  if (!pane) return;
  const item = selectedDecision();
  if (!item) {
    pane.innerHTML = `<div class="empty-state"><p>${app.decisions ? "Nothing needs you." : "Looking for decisions."}</p></div>`;
    return;
  }
  const project = item.projectId ? (app.state.projects || []).find((entry) => entry.id === item.projectId)?.displayName : "";
  const holds = item.holdsUp?.length ? `<p class="decision-holds">Holds up: ${escape(item.holdsUp.map(nameOf).join(", "))}</p>` : "";
  const request = item.answerable ? (item.requests || [])[0] : null;
  const count = request && item.requests.length > 1 ? `<p class="decision-note">1 of ${item.requests.length}</p>` : "";
  const accept = request && recommended(request) ? `<button class="soft-button" type="button" data-decision-accept="${escape(request.id)}" title="Shift+Enter">Accept recommended</button>` : "";
  const failure = app.decisionFailure ? `<p class="decision-note" role="alert">${escape(app.decisionFailure)}</p>` : "";
  pane.innerHTML = `<header class="decision-card-head"><p class="decision-group-name">${escape(decisionGroups.find(([key]) => key === item.group)?.[1] || "")}</p><h2>${escape(nameOf(item.sessionId))}</h2><p>${escape([project, item.since ? timeAgo(item.since) : ""].filter(Boolean).join(" · "))}</p><p>${escape(item.reason)}</p>${holds}</header>${count}${request ? renderRequest(item, request) : item.requestError ? `<p class="decision-note">${escape(item.requestError)}</p>` : ""}${failure}<div class="request-actions">${accept}<button class="soft-button" type="button" data-session="${escape(item.sessionId)}">Open session</button></div>`;
}

export function renderInboxScope() {
  $("#session-list").innerHTML = renderDecisionList();
  renderDecisionPane();
}

export async function respondDecision(requestId, decision, { reload } = {}) {
  const item = selectedDecision();
  if (!item || app.decisionAnswering) return;
  app.decisionAnswering = true;
  app.decisionFailure = "";
  let drop = false;
  try {
    await postAction({ action: "respond", sessionId: item.sessionId, requestId, decision }, crypto.randomUUID());
    drop = true;
    toast("Answered.");
  } catch (error) {
    drop = /already answered or has expired|may have reached the session/.test(error.message);
    app.decisionFailure = /already answered or has expired/.test(error.message) ? "That request was already answered or has expired." : error.message;
  } finally {
    app.decisionAnswering = false;
  }
  if (drop) {
    app.decisionsAnswered = new Set([...(app.decisionsAnswered || []), mark(item.sessionId, requestId)]);
    if (!shownDecisions().some((shown) => shown.sessionId === item.sessionId)) {
      const order = shownDecisions();
      app.decisionSelected = order[0]?.sessionId || null;
    }
  }
  if (app.scope === "inbox") renderInboxScope();
  app.decisionsKey = undefined;
  if (reload) await reload();
}

function decisionRequest(id) {
  return (selectedDecision()?.requests || []).find((request) => request.id === id);
}

export function bindDecisionCard(reload) {
  bindQuestionPager((requestId, decision) => respondDecision(requestId, decision, { reload }), { card: decisionCard, lookup: decisionRequest });
  document.addEventListener("click", async (event) => {
    const select = event.target.closest("[data-decision-select]");
    if (select) {
      app.decisionSelected = select.dataset.decisionSelect;
      app.decisionFailure = "";
      renderInboxScope();
      return;
    }
    const accept = event.target.closest("[data-decision-accept]");
    const choice = event.target.closest("[data-decision-respond]");
    if (accept) {
      const request = decisionRequest(accept.dataset.decisionAccept);
      const answer = recommended(request);
      if (answer) await respondDecision(request.id, answer, { reload });
    } else if (choice) {
      const value = choice.dataset.decisionRespond;
      await respondDecision(choice.dataset.requestId, { choices: [value] }, { reload });
    }
  });
  document.addEventListener("keydown", async (event) => {
    if (app.scope !== "inbox" || !acceptsRecommended(event) || event.target.matches?.("input, textarea, select")) return;
    const request = (selectedDecision()?.answerable && selectedDecision().requests?.[0]) || null;
    const answer = recommended(request);
    if (!answer) return;
    event.preventDefault();
    await respondDecision(request.id, answer, { reload });
  });
}
