import { app } from "./state.js";
import { escape, labels, timeAgo } from "./format.js";
import { shortHostName } from "./hosts.js";
import { sessionButton } from "./session-row.js";
import { projectSessions } from "./session-order.js";
import { projectNow } from "./project-now.js";
import { hostDown, presenceLabel, unavailable } from "./presence.js";

const knownSince = Date.UTC(2000, 0, 1);
const activity = (session) => {
  const at = Date.parse(session.lastActive || "");
  return at >= knownSince ? at : 0;
};

export function sessionRunning(session) {
  return !session.archived && (session.status === "busy" || (session.subagents?.working || 0) > 0);
}

const sessionWorking = (session) => session.status === "busy";

export function leadNote(lead) {
  return lead?.sessionId ? "not running · the next message picks it up" : "starts with the first message sent to it";
}

export function leadSessionID(lead) {
  if (!lead?.sessionId) return "";
  if (!lead.host || lead.host.state === "local" || lead.sessionId.includes("::")) return lead.sessionId;
  return `${lead.host.id}::${lead.sessionId}`;
}

export function projectContents(project) {
  const lead = (project.coordinators || [])[0] || null;
  const leadID = leadSessionID(lead);
  const members = projectSessions("all", project.id).filter((session) => !session.subagent?.parentId && session.id !== leadID);
  return {
    lead,
    leadSession: leadID ? app.state.sessions.find((session) => session.id === leadID) || null : null,
    now: projectNow(members.filter((session) => sessionRunning(session) || session.current), new Set((project.checkouts || []).map((checkout) => checkout.host.id)).size),
    earlier: members.filter((session) => !session.current && !sessionRunning(session)),
  };
}

export function leadActions(session) {
  if (sessionWorking(session)) return [{ kind: "open" }, { kind: "message" }];
  const reachable = !session.unavailableReason;
  return [{ kind: "resume", enabled: reachable, lifecycle: reachable && Boolean(session.capabilities?.lifecycle?.resume) }, { kind: "open" }];
}

export function idleFor(session, now = Date.now()) {
  if (!activity(session)) return "";
  const minutes = Math.max(0, Math.floor((now - activity(session)) / 60000));
  if (minutes < 1) return "now";
  if (minutes < 60) return `${minutes} min ago`;
  if (minutes < 60 * 24) return `${Math.floor(minutes / 60)} hr ago`;
  const days = Math.floor(minutes / (60 * 24));
  return `${days} ${days === 1 ? "day" : "days"} ago`;
}

function compactAge(ms) {
  const minutes = Math.max(0, Math.floor(ms / 60000));
  if (minutes < 1) return "now";
  if (minutes < 60) return `${minutes}m`;
  if (minutes < 60 * 24) return `${Math.floor(minutes / 60)}h`;
  return `${Math.floor(minutes / (60 * 24))}d`;
}

export function rowTime(session, now = Date.now(), running = sessionRunning(session) && !session.waitingOn && !hostDown(session)) {
  if (!running) return activity(session) ? compactAge(now - activity(session)) : "";
  const since = Date.parse(session.activity?.busySince || "");
  return since >= knownSince ? compactAge(now - since) : "now";
}

function card(title, body, trailing = "") {
  return `<section class="project-card"><header><h2>${escape(title)}</h2>${trailing}</header>${body}</section>`;
}

function rows(sessions, empty) {
  return sessions.length
    ? sessions.map((session) => `<div class="project-row">${sessionButton(session)}<span class="project-row-time">${escape(rowTime(session))}</span></div>`).join("")
    : `<p class="project-empty">${escape(empty)}</p>`;
}

function nowRows(now) {
  if (!now.count) return rows([], "Nothing in the last day");
  return now.groups.map((group) => `${now.showsGroups ? `<h3 class="project-now-group">${escape(group.title)}</h3>` : ""}${rows(group.sessions, "")}`).join("");
}

function leadCard(project, contents) {
  const { lead, leadSession } = contents;
  if (!lead) {
    return card("Lead", `<p class="project-lead-name">No lead yet</p><p class="project-note">A lead keeps this project's work in one conversation you can return to. Messages to it always land.</p><div class="project-actions"><button class="primary-button" type="button" data-project-lead="${escape(project.id)}">Start a lead</button></div>`);
  }
  if (!leadSession) {
    return card("Lead", `<p class="project-lead-name">@${escape(lead.name)}</p><p class="project-note">${escape(labels[lead.surface] || lead.surface)} · ${leadNote(lead)}</p>`);
  }
  const where = leadSession.host && leadSession.host.state !== "local" ? shortHostName(leadSession.host.name) : "";
  const blocked = leadSession.waitingOn || unavailable(leadSession);
  const working = sessionWorking(leadSession) && !blocked;
  const status = working ? `Working ${rowTime(leadSession, Date.now(), true)}` : [blocked ? presenceLabel(leadSession) : "Idle", idleFor(leadSession)].filter(Boolean).join(" · ");
  const id = escape(leadSession.id);
  const buttons = leadActions(leadSession).map((action, index) => {
    const style = index === 0 ? "primary-button" : "soft-button";
    if (action.kind === "resume") return `<button class="${style}" type="button" data-project-resume="${id}" data-lifecycle="${action.lifecycle}"${action.enabled ? "" : " disabled"}>Resume</button>`;
    return `<button class="${style}" type="button" data-session="${id}">${action.kind === "open" ? "Open" : "Message"}</button>`;
  }).join("");
  const offline = !working && leadSession.unavailableReason ? `<p class="project-note">Resume needs ${escape(where || "its computer")} to be reachable.</p>` : "";
  const replaced = lead.replaced ? `<p class="project-note" data-project-lead-replaced>Replaced ${escape(timeAgo(lead.replaced.at))} by ${escape(lead.replaced.changedBy)}: ${escape(lead.replaced.reason)}</p>` : "";
  return card("Lead", `<p class="project-lead-name">@${escape(lead.name)}</p><p class="project-note" data-project-lead-status>${escape([labels[leadSession.surface] || leadSession.surface, where, status].filter(Boolean).join(" · "))}</p>${replaced}<div class="project-actions">${buttons}</div>${offline}`);
}

function folders(project) {
  const lines = (project.checkouts || []).map((checkout) => `<li><span>${escape(checkout.host.state === "local" ? "this computer" : shortHostName(checkout.host.name))}</span><code>${escape(checkout.rootPath)}</code></li>`).join("");
  return card("Folders", `<ul class="project-folders">${lines}</ul>`);
}

export function renderProjectView(project) {
  const contents = projectContents(project);
  const computers = new Set((project.checkouts || []).map((checkout) => checkout.host.id)).size;
  const count = contents.now.count + contents.earlier.length + (contents.leadSession ? 1 : 0);
  const id = escape(project.id);
  const firstCheckout = (project.checkouts || []).findIndex((checkout) => !checkout.archived);
  const showEarlier = app.projectEarlier === project.id;
  const left = [
    card(contents.now.count ? `Now · ${contents.now.count}` : "Now", nowRows(contents.now), showEarlier ? "" : `<button class="link-button" type="button" data-project-earlier="${id}">Show earlier</button>`),
    showEarlier ? card("Earlier", rows(contents.earlier, "No earlier sessions")) : "",
  ].join("");
  return `<header class="project-view-header"><h1>${escape(project.displayName)}</h1><span class="project-note">${count} ${count === 1 ? "session" : "sessions"}${computers > 1 ? ` on ${computers} computers` : ""}</span><div class="project-actions"><button class="soft-button" type="button" data-project-lead="${id}">${contents.lead ? "Change lead…" : "New lead…"}</button>${firstCheckout >= 0 ? `<button class="primary-button" type="button" data-project-start="${id}" data-checkout="${firstCheckout}">New session</button>` : ""}</div></header><div class="project-view-columns"><div class="project-view-main">${left}</div><div class="project-view-side">${leadCard(project, contents)}${folders(project)}</div></div>`;
}
