import { app } from "./state.js";
import { $ } from "./dom.js";
import { escape, timeAgo } from "./format.js";
import { displayName } from "./session-labels.js";
import { decisionGroups, decisionsKey, loadDecisions } from "./decisions.js";
import { currentStops, renderInboxScope } from "./decision-card.js";
import { renderTiles } from "./project-tiles-view.js";

export async function refreshDecisions(loader = loadDecisions) {
  const key = decisionsKey();
  if (app.decisionsKey === key) return;
  if (app.decisionsLoading !== undefined) {
    if (app.decisionsLoading !== key) app.decisionsAgain = true;
    return;
  }
  app.decisionsLoading = key;
  app.decisionsAgain = false;
  try {
    const loaded = await loader();
    const current = !app.decisionsAgain && decisionsKey() === key;
    if (current || app.decisionsDropped) {
      app.decisions = loaded;
      app.decisionsKey = current ? key : undefined;
      app.decisionsDropped = false;
      renderInbox();
      renderTiles();
      if (app.scope === "inbox") renderInboxScope();
    } else {
      app.decisionsDropped = true;
    }
  } finally {
    app.decisionsLoading = undefined;
  }
  if (app.decisionsAgain || decisionsKey() !== key) {
    app.decisionsAgain = false;
    await refreshDecisions(loader);
  }
}

function nameOf(id) {
  const session = (app.state.sessions || []).find((item) => item.id === id);
  return session ? displayName(session) : id;
}

function projectOf(id) {
  return id ? (app.state.projects || []).find((project) => project.id === id)?.displayName || "" : "";
}

function attentionItem(item) {
  const message = (app.state.queue || []).find((queued) => queued.id === item.queueId)?.message;
  const text = message ? `<p class="queue-message" data-queued-text>${escape(message)}</p>` : "";
  return `<article class="attention-item"><div><div class="attention-target"><i></i>${escape(item.target)}</div><strong>${escape(item.reason)}</strong>${text}<p>${escape(item.requestedAction)} · ${timeAgo(item.createdAt)}</p></div><div class="operation-actions"><button class="soft-button" data-retry="${item.queueId}" type="button">Retry</button><button class="soft-button" data-cancel="${item.queueId}" type="button">Cancel</button></div></article>`;
}

function leadReplacementItem(item) {
  const by = item.changedBy ? ` by ${item.changedBy}` : "";
  return `<article class="attention-item"><div><div class="attention-target">@${escape(item.name)}</div><strong>Now runs in a new conversation</strong><p>${escape(item.reason)}</p><p>Replaced${escape(by)} · ${timeAgo(item.at)}</p></div><div class="operation-actions"><button class="soft-button" data-session="${escape(item.sessionId)}" type="button">Open</button><button class="soft-button" data-lead-replacement-ack="${escape(item.id)}" type="button">Acknowledge</button></div></article>`;
}

function deliveryProblemItem(item) {
  return `<article class="attention-item"><div><div class="attention-target">${escape(nameOf(item.sessionId))}</div><strong>${escape(item.reason)}</strong><p>${escape(item.message)}</p><p>${escape(item.status)} · ${timeAgo(item.at)}</p></div><div class="operation-actions">${item.deletable
    ? `<button class="soft-button" data-delivery-delete="${escape(item.deliveryId)}" type="button">Delete</button>`
    : `<button class="soft-button" data-delivery-dismiss="${escape(item.deliveryId)}" type="button">Dismiss</button>`}</div></article>`;
}

function decisionItem(item) {
  const place = [projectOf(item.projectId), nameOf(item.sessionId)].filter(Boolean).join(" · ");
  const request = (item.requests || [])[0];
  const headline = request ? (request.kind === "ask" ? request.title || request.prompt : request.prompt || request.title || request.tool) : item.reason;
  const holds = item.holdsUp?.length ? `<span class="decision-holds">Holds up ${item.holdsUp.length}</span>` : "";
  const more = (item.requests || []).length > 1 ? ` · ${item.requests.length} requests` : "";
  const since = item.since ? ` · ${timeAgo(item.since)}` : "";
  const open = item.answerable
    ? `<button class="primary-button" data-open-decision="${escape(item.sessionId)}" type="button">Answer</button>`
    : `<button class="soft-button" data-session="${escape(item.sessionId)}" type="button">Open</button>`;
  return `<article class="attention-item" data-decision="${escape(item.sessionId)}"><div><div class="attention-target">${escape(place)}</div><strong>${escape(headline || item.reason)}</strong><p>${holds}${escape(item.reason)}${escape(more)}${escape(since)}</p></div><div class="operation-actions">${open}</div></article>`;
}

const stopRow = { queue: attentionItem, delivery: deliveryProblemItem, replacement: leadReplacementItem, decision: decisionItem };

function group(title, rows) {
  return rows.length ? `<section class="decision-group"><h3>${escape(title)} · ${rows.length}</h3>${rows.join("")}</section>` : "";
}

export function renderInbox() {
  const panel = $("#inbox-panel");
  const inbox = app.decisions || { leadHandling: [], unreachable: [], outdated: [] };
  const stops = currentStops();
  const stopped = stops.stopped.map((stop) => stopRow[stop.kind](stop.item));
  const groups = decisionGroups.map(([key, title]) => group(title, key === "stopped" ? stopped : stops.decisions.filter((item) => item.group === key).map(decisionItem)));
  const leads = inbox.leadHandling.map((item) => `<p class="decision-lead" data-lead-handled="${escape(item.sessionId)}">${escape(nameOf(item.sessionId))}: ${escape(nameOf(item.heldBy))} is answering its prompt</p>`);
  const notes = [
    ...inbox.unreachable.map((name) => `<p class="decision-note">${escape(name)} isn't answering. Its decisions aren't shown.</p>`),
    ...inbox.outdated.map((name) => `<p class="decision-note">Update Agenthail on ${escape(name)} to see its decisions here.</p>`),
  ];
  const body = groups.join("") + (leads.length ? `<section class="decision-group quiet"><h3>Handled by a lead</h3>${leads.join("")}</section>` : "") + notes.join("");
  $("#inbox-list").innerHTML = body;
  panel.hidden = body === "";
}

export function renderLeadReplacements() {
  renderInbox();
}

export function renderDeliveryProblems() {
  renderInbox();
}
