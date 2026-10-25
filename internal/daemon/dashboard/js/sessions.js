import { app } from "./state.js";
import { $ } from "./dom.js";
import { renderProjects } from "./projects.js";
import { scopedSessions } from "./session-scope.js";
import { displayName, rawDisplayName } from "./session-labels.js";
import { remoteHostName } from "./hosts.js";
import { backgroundWaiting } from "./presence.js";
import { sessionButton } from "./session-row.js";
import { escape, labels } from "./format.js";

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
  if (app.inboxMode === "projects") {
    const projects = renderProjects(query);
    $("#conversation-count").textContent = `${projects.count}`;
    $("#session-tools").classList.remove("hidden");
    $("#session-filters").hidden = true;
    $("#show-archived-row").hidden = true;
    syncInboxModeButtons();
    $("#filter-summary").textContent = projects.count ? `${projects.count} ${projects.count === 1 ? "project" : "projects"}, most recent first` : "No projects yet";
    $("#session-list").innerHTML = projects.html || '<div class="empty-state compact"><p>Sessions in a Git repository or an added folder appear here.</p></div>';
    $("#session-more").hidden = true;
    return;
  }
  $("#session-filters").hidden = false;
  $("#show-archived-row").hidden = false;
  const sessions = scopedSessions().filter((session) => {
    const matchesQuery =
      !query ||
      `${displayName(session)} ${session.surface} ${session.name} ${remoteHostName(session)}`
        .toLowerCase()
        .includes(query);
    const matchesSurface =
      app.filters.surface === "all" || session.surface === app.filters.surface;
    const matchesStatus =
      app.filters.status === "all" ||
      session.status === app.filters.status ||
      (app.filters.status === "background" && backgroundWaiting(session)) ||
      (app.filters.status === "unavailable" &&
        ["unknown", "notLoaded"].includes(session.status));
    return matchesQuery && matchesSurface && matchesStatus;
  });
  const visible = sessions.slice(0, app.sessionLimit);
  const historyResults = app.codexSearch.query === query ? app.codexSearch.results : [];
  $("#conversation-count").textContent = `${sessions.length}`;
  $("#session-tools").classList.toggle("hidden", app.inboxMode !== "all");
  syncInboxModeButtons();
  $("#filter-summary").textContent =
    app.inboxMode === "current"
      ? sessions.length
        ? `Open Claude sessions, working or queued work, and recent sessions (Codex ${app.state.codexRecentHours || 5}-hour window)`
        : "Nothing current right now"
      : sessions.length > visible.length
        ? `Showing ${visible.length} of ${sessions.length}`
        : `${sessions.length} conversations`;
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
  more.hidden = app.inboxMode !== "all" || visible.length >= sessions.length;
  more.textContent = `Show more (${sessions.length - visible.length})`;
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
