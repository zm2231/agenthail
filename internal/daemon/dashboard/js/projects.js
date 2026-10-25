import { app } from "./state.js";
import { escape } from "./format.js";
import { remoteHostName, shortHostName } from "./hosts.js";
import { coordinatorChip } from "./project-model.js";
import { sessionButton } from "./session-row.js";
import { displayName } from "./session-labels.js";

export const collapsedProjectSessions = 5;

export function sessionNeedsYou(session) {
  return session.status === "busy" || (session.subagents?.working || 0) > 0 || session.queueCount > 0 || (app.state.attention || []).some((item) => item.sessionId === session.id);
}

export function projectSection(project, sessions) {
  const expanded = app.expandedProjects.has(project.id);
  const ordered = [...sessions].sort((a, b) => Date.parse(b.lastActive || 0) - Date.parse(a.lastActive || 0));
  const shown = expanded ? ordered : ordered.filter((session, index) => index < collapsedProjectSessions || sessionNeedsYou(session));
  const hidden = ordered.length - shown.length;
  const host = (project.checkouts || []).filter((checkout) => checkout.host.state !== "local").map((checkout) => `<span class="host-badge" title="${escape(`On ${checkout.host.name}${checkout.archived ? " (archived there)" : ""}`)}">${escape(shortHostName(checkout.host.name))}</span>`).join("");
  const toggle = project.archived ? "project-unarchive" : "project-archive";
  return `<section class="project-group${project.archived ? " archived" : ""}"><header class="project-header"><span class="project-name">${escape(project.displayName)}</span><span class="project-count" title="${ordered.length} ${ordered.length === 1 ? "session" : "sessions"}">${ordered.length}</span>${host}${coordinatorChip(project.coordinators || [])}${project.archived ? "" : `<button class="link-button" type="button" data-project-message="${escape(project.id)}" data-project-name="${escape(project.displayName)}" title="${escape(`Send one message to every active conversation in ${project.displayName}`)}">Message</button>`}<button class="link-button" type="button" data-project-action="${toggle}" data-project-id="${escape(project.id)}">${project.archived ? "Unarchive" : "Archive"}</button>${projectMenu(project)}</header>${shown.map(sessionButton).join("")}${hidden > 0 || expanded && ordered.length > collapsedProjectSessions ? `<button class="link-button project-more" type="button" data-project-expand="${escape(project.id)}">${expanded ? "Show less" : `Show ${hidden} more`}</button>` : ""}</section>`;
}

export function projectMenu(project) {
  const checkouts = project.checkouts || [];
  const several = new Set(checkouts.map((checkout) => checkout.host?.id)).size > 1;
  const where = (checkout) => (several || checkout.host?.state !== "local" ? ` on ${shortHostName(checkout.host?.name)}` : "");
  const id = escape(project.id);
  const items = [];
  if (checkouts.some((checkout) => checkout.added)) items.push(`<button type="button" data-project-rename="${id}">Rename…</button>`);
  if (!project.archived) {
    checkouts.forEach((checkout, index) => {
      if (!checkout.archived) items.push(`<button type="button" data-project-start="${id}" data-checkout="${index}">New conversation${escape(where(checkout))}</button>`);
    });
    items.push(`<button type="button" data-project-lead="${id}">${(project.coordinators || []).length ? "Change lead…" : "Set lead…"}</button>`);
    if ((project.coordinators || []).length === 1) items.push(`<button type="button" data-project-clear-lead="${id}">Clear lead</button>`);
  }
  checkouts.forEach((checkout) => {
    if (checkout.added) items.push(`<button type="button" data-project-action="project-remove" data-project-id="${escape(checkout.projectId)}">Remove${escape(where(checkout))}</button>`);
  });
  return `<details class="project-menu"><summary class="link-button" aria-label="${escape(`More for ${project.displayName}`)}">…</summary><div class="project-menu-items" data-pane="float" role="menu">${items.join("")}</div></details>`;
}

export function renderProjects(query) {
  const byProject = new Map();
  const projectArchived = new Map((app.state.projects || []).map((project) => [project.id, project.archived]));
  for (const session of app.state.sessions) {
    if (!session.project) continue;
    if (session.archived && !projectArchived.get(session.project.id)) continue;
    if (query && !`${displayName(session)} ${session.project.displayName} ${remoteHostName(session)}`.toLowerCase().includes(query)) continue;
    if (!byProject.has(session.project.id)) byProject.set(session.project.id, []);
    byProject.get(session.project.id).push(session);
  }
  const projects = app.state.projects || [];
  const matches = (project) => !query || byProject.has(project.id) || project.displayName.toLowerCase().includes(query);
  const active = projects.filter((project) => !project.archived && matches(project));
  const archived = projects.filter((project) => project.archived && matches(project));
  const sections = active.map((project) => projectSection(project, byProject.get(project.id) || [])).join("");
  const archivedSections = archived.length
    ? `<details class="archived-projects" ${app.openArchivedProjects ? "open" : ""}><summary>Archived (${archived.length})</summary>${archived.map((project) => projectSection(project, byProject.get(project.id) || [])).join("")}</details>`
    : "";
  return { html: '<div class="projects-toolbar"><button class="link-button" type="button" data-project-new>Add project…</button></div>' + sections + archivedSections, count: active.length };
}
