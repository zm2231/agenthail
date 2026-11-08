import { app } from "./state.js";
import { $ } from "./dom.js";
import { renderProjects } from "./projects.js";
import { scopedSessions } from "./session-scope.js";
import { listedTotal, listingHasMore, listOrderScopes, orderReady, scopeOrderKeys } from "./session-order.js";
import { displayName, rawDisplayName } from "./session-labels.js";
import { remoteHostName } from "./hosts.js";
import { sessionButton } from "./session-row.js";
import { escape, labels } from "./format.js";
import { scopeKeeps } from "./pane-scope.js";
import { renderInboxScope } from "./decision-card.js";

export const sessionBatchSize = () =>
  window.matchMedia("(max-width: 800px)").matches ? 8 : 80;

export function renderSessions() {
  const mobileViewport = window.matchMedia("(max-width: 800px)").matches;
  if (app.sessionViewport !== mobileViewport) {
    app.sessionViewport = mobileViewport;
    app.sessionLimit = sessionBatchSize();
  }
  if (!app.sessionLimit) app.sessionLimit = sessionBatchSize();
  const query = $("#session-search").value.trim().toLowerCase();
  const counts = app.state.counts || {};
  document.querySelectorAll("[data-inbox-count]").forEach((badge) => {
    const value = badge.dataset.inboxCount === "projects" ? (app.state.projects || []).filter((project) => !project.archived).length : counts[badge.dataset.inboxCount];
    badge.textContent = value === undefined ? "" : ` ${value}`;
  });
  const scopeKeys = scopeOrderKeys();
  const scopedProject = scopeKeys.length > 0;
  if (!orderReady(scopedProject ? scopeKeys : listOrderScopes())) {
    syncInboxModeButtons();
    $("#conversation-count").textContent = "";
    $("#filter-summary").textContent = "Loading";
    $("#session-list").innerHTML = '<div class="empty-state compact"><p>Loading this list.</p></div>';
    $("#session-more").hidden = true;
    return;
  }
  if (app.scope === "inbox") {
    $("#session-tools").classList.add("hidden");
    $("#filter-summary").textContent = "";
    $("#session-more").hidden = true;
    renderInboxScope();
    return;
  }
  if (app.inboxMode === "projects" && !scopedProject) {
    const projects = renderProjects(query);
    $("#conversation-count").textContent = `${projects.count}`;
    $("#session-tools").classList.remove("hidden");
    $("#session-filters").hidden = true;
    $("#show-archived-row").hidden = true;
    syncInboxModeButtons();
    $("#filter-summary").textContent = projects.count ? `${projects.count} ${projects.count === 1 ? "project" : "projects"}, active first` : "No projects yet";
    $("#session-list").innerHTML = projects.html || '<div class="empty-state compact"><p>Sessions in a Git repository or an added folder appear here.</p></div>';
    $("#session-more").hidden = true;
    return;
  }
  $("#session-filters").hidden = false;
  $("#show-archived-row").hidden = false;
  const paged = app.inboxMode === "all" || scopedProject;
  const sessions = paged ? scopedSessions() : scopedSessions().filter((session) =>
    !query || `${displayName(session)} ${session.surface} ${session.name} ${remoteHostName(session)}`.toLowerCase().includes(query));
  const visible = sessions.slice(0, app.sessionLimit);
  const listScopes = listOrderScopes();
  const total = scopedProject ? sessions.length : paged ? listScopes.reduce((sum, key) => sum + (listedTotal(key) ?? 0), 0) : sessions.length;
  const hasMore = visible.length < sessions.length || (paged && !scopedProject && listScopes.some(listingHasMore));
  const historyResults = (app.codexSearch.query === query ? app.codexSearch.results : []).filter((result) => scopeKeeps(app.scope, result.project?.id));
  $("#conversation-count").textContent = `${total}`;
  $("#session-tools").classList.toggle("hidden", !paged);
  syncInboxModeButtons();
  $("#filter-summary").textContent =
    app.inboxMode === "current"
      ? sessions.length
        ? `Open Claude sessions, working or queued work, and recent sessions (Codex ${app.state.codexRecentHours || 5}-hour window)`
        : "Nothing current right now"
      : scopedProject
        ? `${total} ${total === 1 ? "conversation" : "conversations"} in this project${hasMore ? " so far" : ""}`
        : hasMore
          ? `Showing ${visible.length} of ${total}`
          : `${total} conversations`;
  const sessionItems = visible.map(sessionButton).join("");
  const historyItems = historyResults
    .map(
      (result) =>
        `<button class="session" title="${escape(rawDisplayName(result))}" type="button" data-session="${escape(result.id)}"><div class="session-name"><i class="dot recent"></i><span>${escape(displayName(result))}</span></div><div class="session-detail"><span>Codex history</span><span>${escape(result.snippet || "Matched conversation")}</span></div></button>`,
    )
    .join("");
  const searchStatus = app.codexSearch.loading
    ? '<div class="filter-summary">Searching Codex history…</div>'
    : app.codexSearch.error
      ? `<div class="filter-summary">${escape(app.codexSearch.error)}</div>`
      : historyItems
        ? '<div class="filter-summary">Codex history matches</div>'
        : "";
  $("#session-list").innerHTML =
    sessionItems || historyItems || searchStatus
      ? `${sessionItems}${searchStatus}${historyItems}`
      : `<div class="empty-state compact"><p>${app.inboxMode === "current" ? "No current work. Search to find an older Codex conversation." : "No conversations in this view."}</p></div>`;
  const more = $("#session-more");
  more.hidden = !hasMore;
  more.textContent = scopedProject ? "Show more" : `Show more (${Math.max(0, total - visible.length)})`;
}

export function syncInboxModeButtons() {
  document.querySelectorAll("[data-inbox-mode]").forEach((button) => {
    const active = button.dataset.inboxMode === app.inboxMode;
    button.classList.toggle("active", active);
    button.setAttribute("aria-pressed", String(active));
  });
}

export function renderSurfaceFilter() {
  const select = $("#session-surface-filter");
  const current = app.filters.surface;
  const options = [
    '<option value="all">All surfaces</option>',
    ...app.state.surfaces.map(
      (surface) =>
        `<option value="${escape(surface.name)}">${escape(labels[surface.name] || surface.name)}</option>`,
    ),
  ];
  select.innerHTML = options.join("");
  select.value = app.state.surfaces.some((surface) => surface.name === current)
    ? current
    : "all";
  app.filters.surface = select.value;
  $("#session-status-filter").value = app.filters.status;
}
