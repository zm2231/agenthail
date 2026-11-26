import { app } from "./state.js";
import { escape } from "./format.js";
import { answeredMark, recommended, shownDecisions } from "./decisions.js";

const questionKinds = ["question", "ask"];

// inlineAnswer is how a waiting Now row can be answered without leaving the
// project: an approval whose request fits one line, a single question with a
// recommended option, or else Answer, which opens the whole request.
export function inlineAnswer(session, decisions = shownDecisions()) {
  if (!session.waitingOn) return null;
  const item = decisions.find((entry) => entry.sessionId === session.id && entry.answerable);
  if (!item) return null;
  const requests = item.requests || [];
  const request = requests.length === 1 ? requests[0] : null;
  if (request && !questionKinds.includes(request.kind) && request.kind !== "input" && request.summary && !request.noAllow) {
    return { kind: "allow", sessionId: session.id, request };
  }
  const answer = request && request.questions?.length === 1 ? recommended(request) : null;
  if (answer) {
    const question = request.questions[0];
    return { kind: "recommended", sessionId: session.id, request, question: question.question, label: question.options[question.suggested].label, answer };
  }
  return { kind: "answer", sessionId: session.id, reason: item.reason || "Waiting on you", tool: request && !questionKinds.includes(request.kind) ? request.tool : "" };
}

function button(style, label, attributes) {
  return `<button class="${style}" type="button" ${attributes}>${escape(label)}</button>`;
}

export function renderInlineAnswer(session, decisions) {
  const offer = inlineAnswer(session, decisions);
  if (!offer) return "";
  const target = `data-session-id="${escape(offer.sessionId)}" data-request-id="${escape(offer.request?.id || "")}"`;
  const failure = app.nowAnswerFailure?.sessionId === offer.sessionId ? `<p class="project-note" role="alert">${escape(app.nowAnswerFailure.message)}</p>` : "";
  if (offer.kind === "allow") {
    const { request } = offer;
    const detail = request.detail && request.detail !== request.summary ? `<details class="project-now-detail"><summary>Details</summary><pre>${escape(request.detail)}</pre></details>` : "";
    return `<div class="project-now-answer"><p class="project-now-ask"><span class="project-now-tool">${escape(request.tool || "Tool")}</span><code>${escape(request.summary)}</code></p>${detail}<div class="project-actions">${button("primary-button", "Allow", `data-now-respond="allow" ${target}`)}${button("soft-button", "Deny", `data-now-respond="deny" ${target}`)}</div>${failure}</div>`;
  }
  if (offer.kind === "recommended") {
    return `<div class="project-now-answer"><p class="project-now-ask">${escape(offer.question)}</p><div class="project-actions">${button("primary-button", offer.label, `data-now-accept ${target}`)}${button("soft-button", "Answer", `data-open-decision="${escape(offer.sessionId)}"`)}</div>${failure}</div>`;
  }
  const tool = offer.tool ? `<span class="project-now-tool">${escape(offer.tool)}</span>` : "";
  return `<div class="project-now-answer"><p class="project-now-ask">${tool}<span>${escape(offer.reason)}</span></p><div class="project-actions">${button("soft-button", "Answer", `data-open-decision="${escape(offer.sessionId)}"`)}</div></div>`;
}

// answerInPlace posts the person's answer for a row's request. A request
// already answered elsewhere or expired drops from the row like an answered one.
export async function answerInPlace(sessionId, requestId, decision, post) {
  if (app.nowAnswering) return false;
  app.nowAnswering = true;
  app.nowAnswerFailure = null;
  let drop = false;
  try {
    await post({ action: "respond", sessionId, requestId, decision }, crypto.randomUUID());
    drop = true;
  } catch (error) {
    const stale = /already answered or has expired/.test(error.message);
    drop = stale || /may have reached the session/.test(error.message);
    app.nowAnswerFailure = { sessionId, message: stale ? "That request was already answered or has expired." : error.message };
  } finally {
    app.nowAnswering = false;
  }
  if (drop) app.decisionsAnswered = new Set([...(app.decisionsAnswered || []), answeredMark(sessionId, requestId)]);
  return drop;
}

export function nowDecision(target, decisions = shownDecisions()) {
  const item = decisions.find((entry) => entry.sessionId === target.dataset.sessionId);
  const request = (item?.requests || []).find((entry) => entry.id === target.dataset.requestId);
  if (!request) return null;
  if (target.dataset.nowRespond) return { choices: [target.dataset.nowRespond] };
  return target.dataset.nowAccept !== undefined ? recommended(request) : null;
}
