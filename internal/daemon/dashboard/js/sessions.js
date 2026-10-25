import { app } from "./state.js";
import { $ } from "./dom.js";
import { renderProjects } from "./projects.js";
import { viewSessions } from "./session-view.js";
import { sessionButton } from "./session-row.js";
import { escape, labels } from "./format.js";

export function renderSessions() {
  if (!app.sessionView) return;
  const view = app.sessionView.view;
  document.querySelectorAll("[data-inbox-count]").forEach((badge) => {
    const scope = badge.dataset.inboxCount;
    badge.textContent = scope === "projects" ? "" : ` ${view.counts[scope].families}`;
  });
  syncInboxModeButtons();
  $("#session-tools").classList.remove("hidden");
  $("#session-filters").hidden = app.inboxMode === "projects";
  $("#show-archived-row").hidden = false;
  $("#session-more").hidden = true;
  if (app.inboxMode === "projects") {
    $("#conversation-count").textContent = `${view.counts[view.scope].families}`;
    $("#filter-summary").textContent = `${view.counts[view.scope].families} conversations`;
    $("#session-list").innerHTML = renderProjects().html;
    return;
  }
  const sessions = viewSessions(app.sessionView);
  const query = $("#session-search").value.trim().toLowerCase();
  const history = app.codexSearch.query === query ? app.codexSearch.results : [];
  $("#conversation-count").textContent = `${view.counts[view.scope].families}`;
  $("#filter-summary").textContent = `${view.counts[view.scope].families} conversations · ${view.counts[view.scope].sessions} sessions`;
  const historyHeading = app.codexSearch.loading ? "Searching Codex history…" : app.codexSearch.error || (history.length ? "Codex history matches" : "");
  $("#session-list").innerHTML = sessions.map(sessionButton).join("") + (historyHeading ? `<div class="filter-summary">${escape(historyHeading)}</div>` : "") + history.map(sessionButton).join("") || '<div class="empty-state compact"><p>No conversations in this view.</p></div>';
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
