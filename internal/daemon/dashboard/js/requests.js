import { app } from "./state.js";
import { $ } from "./dom.js";
import { responseError } from "./http.js";
import { escape } from "./format.js";
import { waitingPlace } from "./presence.js";
import { displayName } from "./session-labels.js";
import { action } from "./actions.js";
import { toast } from "./feedback.js";
import { load } from "./dashboard-state.js";
import { selectSession } from "./session-selection.js";
import { renderQuestionCard } from "./question-pager.js";
import { bindQuestionPager } from "./question-pager-events.js";

export const permissionModes = [
  ["", "Default (Claude settings)"],
  ["acceptEdits", "Accept edits"],
  ["auto", "Auto"],
  ["plan", "Plan"],
  ["manual", "Manual"],
  ["dontAsk", "Don't ask"],
  ["bypassPermissions", "Bypass permissions"],
];

export async function syncRequests() {
  const session = app.selected;
  const panel = $("#request-panel");
  const capabilities = app.history?.capabilities || {};
  if (!session || !capabilities.respond || app.history?.readOnly || (session.waitingOn && !session.waitingOn.answerable)) {
    app.requests = null;
    app.requestsKey = "";
    panel.innerHTML = session?.waitingOn ? renderElsewhereWait(session) : "";
    panel.hidden = panel.innerHTML === "";
    return;
  }
  const key = `${session.id}|${session.waitingOn?.id || ""}`;
  if (app.requestsKey === key) return;
  app.requestsKey = key;
  try {
    const response = await fetch(`/api/requests?sessionId=${encodeURIComponent(session.id)}`);
    if (!response.ok) throw await responseError(response);
    const state = await response.json();
    if (app.selected?.id !== session.id) return;
    app.requests = state;
  } catch (error) {
    app.requests = { permissionMode: app.requests?.permissionMode || "", requests: [], error: error.message };
  }
  renderRequests();
}

export function renderRequests() {
  const panel = $("#request-panel");
  const state = app.requests || { requests: [] };
  const capabilities = app.history?.capabilities || {};
  const mode = capabilities.permissionMode
    ? `<form class="request-mode" data-permission-mode><label for="permission-mode">Permission mode</label><select id="permission-mode" name="permissionMode">${permissionModes.map(([value, label]) => `<option value="${value}"${value === (state.permissionMode || "") ? " selected" : ""}${value === "" ? " disabled" : ""}>${escape(label)}</option>`).join("")}</select><button class="soft-button" type="submit">Change</button></form>`
    : "";
  const failure = state.error ? `<p role="status">Could not read this session's requests: ${escape(state.error)}</p>` : "";


  const waiting = app.selected?.waitingOn;
  const cards = waiting && !waiting.answerable
    ? renderElsewhereWait(app.selected)
    : (state.requests || []).map(renderRequestCard).join("");
  panel.innerHTML = cards + failure + mode;
  panel.hidden = panel.innerHTML === "";
}

export function renderElsewhereWait(session) {
  const place = waitingPlace(session);
  const asking = { question: "asks you a question", input: "needs your input", dialog: "has a dialog open in its terminal" }[session.waitingOn.kind] || "needs your approval";
  const heading = place === "restart" ? "is waiting on a prompt Agenthail can't reach" : asking;
  const detail = {
    restart: "Nothing shows this prompt, so no one can answer it. Stop and resume the session to clear it; messages you send wait until then.",
    terminal: "Agenthail can't answer this one. Answer it in the session's terminal. Messages you send wait until then.",
  }[place] || "Agenthail can't answer this one. Answer it where the session runs, such as its terminal. Messages you send wait until then.";
  const actions = [];
  if (place === "restart") actions.push('<button class="primary-button" data-action="session-restart" type="button">Stop and resume</button>');
  if (session.runtime?.focusable) actions.push(`<button class="${actions.length ? "soft-button" : "primary-button"}" data-action="session-focus" type="button">Open session</button>`);
  return `<div class="request-card"><h3>${escape(displayName(session))} ${heading}</h3><p>${detail}</p>${actions.length ? `<div class="request-actions">${actions.join("")}</div>` : ""}</div>`;
}

export function renderRequestCard(request) {
  const created = request.createdAt ? ` · <time datetime="${escape(request.createdAt)}">${escape(new Date(request.createdAt).toLocaleTimeString())}</time>` : "";
  if (request.kind === "input" && request.elicitation) return renderInputRequest(request, created);
  if (request.kind === "question") return renderQuestionCard(request, created);
  const title = request.title || `Claude wants to use ${request.tool}`;
  const input = request.input && Object.keys(request.input).length ? `<pre>${escape(JSON.stringify(request.input, null, 2))}</pre>` : "";
  const detail = [request.description, request.reason, request.blockedPath && `Path: ${request.blockedPath}`].filter(Boolean).map((line) => `<p>${escape(line)}</p>`).join("");
  return `<div class="request-card"><h3>${escape(title)}${created}</h3>${detail}${input}<div class="request-actions"><button class="primary-button" type="button" data-request-respond="allow" data-request-id="${escape(request.id)}">Allow</button>${request.alwaysAllow ? `<button class="soft-button" type="button" data-request-respond="always" data-request-id="${escape(request.id)}">Always allow</button>` : ""}<button class="soft-button" type="button" data-request-respond="deny" data-request-id="${escape(request.id)}">Deny</button></div></div>`;
}

export function renderInputRequest(request, created) {
  const e = request.elicitation;
  const heading = `<h3>${escape(e.server || "An MCP server")} asks for input${created}</h3><p>${escape(e.message)}</p>`;
  const decline = `<button class="soft-button" type="button" data-request-respond="deny" data-request-id="${escape(request.id)}">Decline</button>`;
  if (e.mode === "url") {
    const link = /^https?:\/\//i.test(e.url || "") ? `<p><a href="${escape(e.url)}" target="_blank" rel="noopener noreferrer">${escape(e.url)}</a></p>` : `<p>${escape(e.url || "")}</p>`;
    return `<div class="request-card">${heading}${link}<div class="request-actions"><button class="primary-button" type="button" data-request-respond="allow" data-request-id="${escape(request.id)}">Opened, continue</button>${decline}</div></div>`;
  }
  const fields = (e.fields || []).map(renderInputField).join("");
  return `<form class="request-card" data-request-input="${escape(request.id)}">${heading}${fields}<div class="request-actions"><button class="primary-button" type="submit">Submit</button>${decline}</div></form>`;
}

export function renderInputField(field) {
  const label = `${escape(field.title || field.name)}${field.required ? " *" : ""}`;
  const help = field.description ? `<small>${escape(field.description)}</small>` : "";
  const name = `field-${escape(field.name)}`;
  if (field.multiSelect) {
    const picked = new Set(Array.isArray(field.default) ? field.default : []);
    const options = (field.options || []).map((option) => `<label><input type="checkbox" name="${name}" value="${escape(option.value)}"${picked.has(option.value) ? " checked" : ""}><span>${escape(option.label)}</span></label>`).join("");
    return `<fieldset data-field="${escape(field.name)}"><legend>${label}</legend>${help}${options}</fieldset>`;
  }
  if (field.options) {
    const options = (field.options || []).map((option) => `<option value="${escape(option.value)}"${option.value === field.default ? " selected" : ""}>${escape(option.label)}</option>`).join("");
    return `<label data-field="${escape(field.name)}"><span>${label}</span><select name="${name}"><option value=""></option>${options}</select>${help}</label>`;
  }
  if (field.type === "boolean") {
    return `<label data-field="${escape(field.name)}"><input type="checkbox" name="${name}"${field.default === true ? " checked" : ""}><span>${label}</span>${help}</label>`;
  }
  const number = field.type === "number" || field.type === "integer";
  const value = field.default === undefined ? "" : ` value="${escape(String(field.default))}"`;
  return `<label data-field="${escape(field.name)}"><span>${label}</span><input type="${number ? "number" : "text"}"${field.type === "integer" ? ' step="1"' : number ? ' step="any"' : ""} name="${name}"${value}>${help}</label>`;
}

export function inputContent(form, request) {
  const content = {};
  for (const field of request.elicitation.fields || []) {
    const name = `field-${field.name}`;
    if (field.multiSelect) {
      content[field.name] = [...form.querySelectorAll(`[name="${CSS.escape(name)}"]:checked`)].map((input) => input.value);
      continue;
    }
    const input = form.querySelector(`[name="${CSS.escape(name)}"]`);
    if (field.type === "boolean") {
      content[field.name] = input.checked;
      continue;
    }
    const text = input.value.trim();
    if (text === "") continue;
    content[field.name] = field.type === "number" || field.type === "integer" ? Number(text) : text;
  }
  return content;
}

export async function respondToRequest(requestId, decision) {
  try {
    const result = await action("respond", { requestId, decision });
    toast(`Request ${result?.result?.outcome || "answered"}.`);
  } catch (error) {
    const stale = /already answered or has expired/.test(error.message);
    if (stale && app.requests) {
      app.requests = { ...app.requests, requests: (app.requests.requests || []).filter((request) => request.id !== requestId) };
      renderRequests();
    }
    toast(stale ? "That request was already answered or has expired." : error.message);
  }
  app.requestsKey = "";
  await load();
  if (app.selected) await selectSession(app.selected.id);
}

export function bindRequestEvents() {
  document.addEventListener("click", async (event) => {
    const button = event.target.closest("[data-request-respond]");
    if (!button) return;
    button.disabled = true;
    const choice = button.dataset.requestRespond;
    await respondToRequest(button.dataset.requestId, choice === "deny" ? { behavior: "deny" } : { behavior: "allow", always: choice === "always" });
  });
  bindQuestionPager(respondToRequest);
  document.addEventListener("submit", async (event) => {
    const form = event.target.closest("[data-request-input]");
    if (!form) return;
    event.preventDefault();
    const request = (app.requests?.requests || []).find((r) => r.id === form.dataset.requestInput);
    if (!request) return;
    form.querySelector('[type="submit"]').disabled = true;
    await respondToRequest(request.id, { behavior: "allow", content: inputContent(form, request) });
  });
  document.addEventListener("submit", async (event) => {
    const form = event.target.closest("[data-permission-mode]");
    if (!form) return;
    event.preventDefault();
    const permissionMode = form.elements.permissionMode.value;
    if (!permissionMode) return;
    try {
      await action("permission-mode", { permissionMode });
      toast(`Permission mode set to ${permissionMode}.`);
    } catch (error) {
      toast(error.message);
    }
    app.requestsKey = "";
    await syncRequests();
  });
}
