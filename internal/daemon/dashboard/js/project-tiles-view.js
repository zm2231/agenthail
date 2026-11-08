import { app } from "./state.js";
import { $ } from "./dom.js";
import { escape } from "./format.js";
import { orderTiles, tileLetter, needsYouByProject } from "./project-tiles.js";
import { shownDecisions } from "./decision-card.js";
import { openProject, leaveProject } from "./project-pane.js";
import { syncDecisionPane } from "./pane-scope.js";
import { renderSessions } from "./sessions.js";
import { showView } from "./navigation.js";

export const tileLimit = 6;

function tile(scope, mark, title, count, selected) {
  const badge = count ? `<span class="tile-badge">${count}</span>` : "";
  return `<button class="project-tile${selected ? " active" : ""}" type="button" data-scope="${escape(scope)}" title="${escape(title)}" aria-pressed="${selected}"><span class="tile-mark">${mark}${badge}</span><span class="tile-name">${escape(title)}</span></button>`;
}

export function renderTiles() {
  const row = $("#project-tiles");
  if (!row) return;
  const projects = app.state.projects || [];
  const decisions = shownDecisions();
  const counts = needsYouByProject(decisions, projects);
  const { shown, more } = orderTiles(projects, tileLimit);
  const scope = app.scope || "all";
  const moreCount = more.reduce((sum, project) => sum + (counts[project.id] || 0), 0);
  const moreSelected = more.some((project) => project.id === scope);
  const options = more.map((project) => `<option value="${escape(project.id)}"${project.id === scope ? " selected" : ""}>${escape(project.displayName || project.id)}${project.archived ? " (archived)" : ""}</option>`).join("");
  const menu = more.length
    ? `<label class="project-tile${moreSelected ? " active" : ""}" title="More projects"><span class="tile-mark">…${moreCount ? `<span class="tile-badge">${moreCount}</span>` : ""}</span><span class="tile-name">${more.length} more</span><select data-scope-more aria-label="More projects"><option value="">More projects</option>${options}</select></label>`
    : "";
  row.innerHTML = tile("inbox", "▤", "Inbox", decisions.length, scope === "inbox")
    + shown.map((project) => tile(project.id, escape(tileLetter(project.displayName || project.id)), project.displayName || project.id, counts[project.id] || 0, scope === project.id)).join("")
    + menu
    + tile("all", "≡", "All", 0, scope === "all");
}

export function setScope(scope) {
  showView("conversations");
  app.scope = scope;
  if (scope === "inbox" || scope === "all") leaveProject();
  else openProject(scope);
  syncDecisionPane();
  renderSessions();
  renderTiles();
}

export function openDecision(sessionId) {
  app.decisionSelected = sessionId;
  app.decisionFailure = "";
  setScope("inbox");
}

export function dropMissingScope() {
  const scope = app.scope;
  if (!scope || scope === "all" || scope === "inbox") return;
  if ((app.state.projects || []).some((project) => project.id === scope && !project.archived)) return;
  app.scope = "all";
  if (app.selectedProject === scope) leaveProject();
  syncDecisionPane();
}

export function bindTiles() {
  document.addEventListener("click", (event) => {
    const button = event.target.closest("[data-scope]");
    if (button) setScope(button.dataset.scope);
    const open = event.target.closest("[data-open-decision]");
    if (open) openDecision(open.dataset.openDecision);
  });
  document.addEventListener("change", (event) => {
    if (event.target.matches("[data-scope-more]") && event.target.value) setScope(event.target.value);
  });
}
