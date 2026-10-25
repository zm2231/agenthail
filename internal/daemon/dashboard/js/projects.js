import { app } from "./state.js";
import { escape } from "./format.js";
import { remoteHostName, shortHostName } from "./hosts.js";
import { coordinatorChip } from "./project-model.js";
import { sessionButton } from "./session-row.js";
import { viewSessions } from "./session-view.js";

export function projectSection(project, group) {
  const expanded = app.expandedProjects.has(project.id);
  const families = app.sessionView.view.families.filter((family) => family.projectId === group.id);
  const shown = viewSessions(app.sessionView, expanded ? families.map((family) => family.id) : group.collapsedFamilyIds);
  const hidden = group.counts.families - group.collapsedFamilyIds.length;
  const host = (project.checkouts || []).filter((checkout) => checkout.host.state !== "local").map((checkout) => `<span class="host-badge" title="${escape(`On ${checkout.host.name}${checkout.archived ? " (archived there)" : ""}`)}">${escape(shortHostName(checkout.host.name))}</span>`).join("");
  const toggle = project.archived ? "project-unarchive" : "project-archive";
  return `<section class="project-group${project.archived ? " archived" : ""}"><header class="project-header"><span class="project-name">${escape(project.displayName)}</span><span class="project-count" title="${group.counts.families} ${group.counts.families === 1 ? "conversation" : "conversations"}">${group.counts.families}</span>${host}${coordinatorChip(project.coordinators || [])}${project.archived ? "" : `<button class="link-button" type="button" data-project-message="${escape(project.id)}" data-project-name="${escape(project.displayName)}" title="${escape(`Send one message to every active conversation in ${project.displayName}`)}">Message</button>`}<button class="link-button" type="button" data-project-action="${toggle}" data-project-id="${escape(project.id)}">${project.archived ? "Unarchive" : "Archive"}</button>${projectMenu(project)}</header>${shown.map(sessionButton).join("")}${hidden > 0 ? `<button class="link-button project-more" type="button" data-project-expand="${escape(project.id)}">${expanded ? "Show less" : `Show ${hidden} more`}</button>` : ""}</section>`;
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

export function renderProjects() {
  const metadata = new Map(app.state.projects.map((project) => [project.id, project]));
  const groups = app.sessionView.view.projects.filter((group) => group.id !== "");
  const sections = groups.map((group) => {
    const project = metadata.get(group.id);
    if (!project) throw Error(`Missing project metadata: ${group.id}`);
    return projectSection(project, group);
  }).join("");
  return { html: '<div class="projects-toolbar"><button class="link-button" type="button" data-project-new>Add project…</button></div>' + sections };
}
