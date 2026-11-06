import { app } from "./state.js";
import { escape } from "./format.js";
import { shortHostName } from "./hosts.js";
import { coordinatorChip } from "./project-model.js";
import { sessionButton } from "./session-row.js";
import { listedProject, orderedProjectIDs, orderReady, projectKey, projectSessions } from "./session-order.js";

export function projectSection(project, scope) {
  const expanded = app.expandedProjects.has(project.id);
  const full = projectKey(project.id, scope);
  const collapsed = projectSessions(scope, project.id, true);
  const shown = expanded && orderReady([full]) ? projectSessions(full, project.id) : collapsed;
  const total = listedProject(scope, project.id)?.counts?.sessions ?? collapsed.length;
  const hidden = total - shown.length;
  const host = (project.checkouts || []).filter((checkout) => checkout.host.state !== "local").map((checkout) => `<span class="host-badge" title="${escape(`On ${checkout.host.name}${checkout.archived ? " (archived there)" : ""}`)}">${escape(shortHostName(checkout.host.name))}</span>`).join("");
  const toggle = project.archived ? "project-unarchive" : "project-archive";
  return `<section class="project-group${project.archived ? " archived" : ""}"><header class="project-header${app.selectedProject === project.id ? " selected" : ""}"><button class="project-name" type="button" data-project-open="${escape(project.id)}" title="${escape(`Open ${project.displayName}`)}">${escape(project.displayName)}</button><span class="project-count" title="${total} ${total === 1 ? "session" : "sessions"}">${total}</span>${host}${coordinatorChip(project.coordinators || [])}${project.archived ? "" : `<button class="link-button" type="button" data-project-message="${escape(project.id)}" data-project-name="${escape(project.displayName)}" title="${escape(`Send one message to every active conversation in ${project.displayName}`)}">Message</button>`}<button class="link-button" type="button" data-project-action="${toggle}" data-project-id="${escape(project.id)}">${project.archived ? "Unarchive" : "Archive"}</button>${projectMenu(project)}</header>${shown.map(sessionButton).join("")}${hidden > 0 || expanded && total > collapsed.length ?`<button class="link-button project-more" type="button" data-project-expand="${escape(project.id)}">${expanded ? "Show less" : `Show ${hidden} more`}</button>` : ""}</section>`;
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

function inDaemonOrder(projects, scope) {
  const byID = new Map(projects.map((project) => [project.id, project]));
  const ordered = orderedProjectIDs(scope).filter((id) => byID.has(id)).map((id) => byID.get(id));
  const placed = new Set(ordered.map((project) => project.id));
  return [...ordered, ...projects.filter((project) => !placed.has(project.id))];
}

export function renderProjects(query) {
  const scopeOf = (project) => (project.archived ? "archived" : "all");
  const projects = app.state.projects || [];
  const matches = (project) => !query || project.displayName.toLowerCase().includes(query) || (app.listQuery && listedProject(scopeOf(project), project.id));
  const active = inDaemonOrder(projects.filter((project) => !project.archived && matches(project)), "all");
  const archived = inDaemonOrder(projects.filter((project) => project.archived && matches(project)), "archived");
  const sections = active.map((project) => projectSection(project, "all")).join("");
  const archivedSections = archived.length
    ? `<details class="archived-projects" ${app.openArchivedProjects ? "open" : ""}><summary>Archived (${archived.length})</summary>${archived.map((project) => projectSection(project, "archived")).join("")}</details>`
    : "";
  return { html: '<div class="projects-toolbar"><button class="link-button" type="button" data-project-new>Add project…</button></div>' + sections + archivedSections, count: active.length };
}
