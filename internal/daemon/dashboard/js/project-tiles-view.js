import { app } from "./state.js";
import { $ } from "./dom.js";
import { escape } from "./format.js";
import { arrangeTiles, fillTiles, orderTiles, tileColumns, tileLetter, tileLeads, projectColour, projectColourID } from "./project-tiles.js";
import { changeTiles, onTileOrderChange, seedTileOrder, tileOrder } from "./tile-order.js";
import { currentStops } from "./decision-card.js";
import { stopsByProject } from "./inbox-stops.js";
import { openProject, leaveProject } from "./project-pane.js";
import { syncDecisionPane } from "./pane-scope.js";
import { renderSessions } from "./sessions.js";
import { showView } from "./navigation.js";

let shownColumns = 0;
let arranged = [];
const tileType = "text/x-agenthail-tile";

function tile(scope, mark, title, count, selected, colour, movable = false) {
  const badge = count ? `<span class="tile-badge">${count}</span>` : "";
  const tint = colour === undefined ? "" : ` data-colour="${colour}"`;
  const move = movable ? ` data-tile="${escape(scope)}" draggable="true" aria-keyshortcuts="Alt+ArrowLeft Alt+ArrowRight"` : "";
  return `<button class="project-tile${selected ? " active" : ""}" type="button" data-scope="${escape(scope)}"${move} title="${escape(title)}" aria-pressed="${selected}"><span class="tile-mark"${tint}>${mark}${badge}</span><span class="tile-name">${escape(title)}</span></button>`;
}

export function renderTiles() {
  const row = $("#project-tiles");
  if (!row) return;
  const projects = app.state.projects || [];
  const stops = currentStops();
  const sessions = new Map((app.state.sessions || []).map((session) => [session.id, session]));
  const counts = stopsByProject(stops, projects, (id) => sessions.get(id)?.project?.id);
  const leads = tileLeads(projects, app.state.sessions || []);
  shownColumns = tileColumns(row.clientWidth);
  if (projects.length && !tileOrder().order.length) {
    const seen = orderTiles(projects, projects.length, { waiting: counts, leads });
    seedTileOrder([...seen.shown, ...seen.more].map((project) => project.id));
  }
  const byOrder = ({ order, pinned }) => fillTiles(shownColumns, (limit) => arrangeTiles(projects, limit, order, pinned));
  const { shown, more } = tileOrder().order.length ? byOrder(tileOrder()) : fillTiles(shownColumns, (limit) => orderTiles(projects, limit, { waiting: counts, leads }));
  arranged = [...shown, ...more].map((project) => project.id);
  const scope = app.scope || "all";
  const owner = app.state.daemon?.host?.id || "";
  const moreCount = more.reduce((sum, project) => sum + (counts[project.id] || 0), 0);
  const moreSelected = more.some((project) => project.id === scope);
  const options = more.map((project) => `<option value="${escape(project.id)}"${project.id === scope ? " selected" : ""}>${escape(project.displayName || project.id)}${project.archived ? " (archived)" : ""}</option>`).join("");
  const menu = more.length
    ? `<label class="project-tile${moreSelected ? " active" : ""}" title="${more.length} more ${more.length === 1 ? "project" : "projects"}"><span class="tile-mark">…${moreCount ? `<span class="tile-badge">${moreCount}</span>` : ""}</span><span class="tile-name">+${more.length}</span><select data-scope-more aria-label="More projects"><option value="">More projects</option>${options}</select></label>`
    : "";
  const held = globalThis.document?.activeElement;
  const active = held && row.contains?.(held) ? held : null;
  const focused = active?.matches("[data-scope-more]") ? "select[data-scope-more]" : active?.dataset.scope !== undefined ? `button[data-scope="${CSS.escape(active.dataset.scope)}"]` : null;
  row.innerHTML = tile("inbox", "▤", "Inbox", stops.count, scope === "inbox")
    + shown.map((project) => tile(project.id, escape(tileLetter(project.displayName || project.id)), project.displayName || project.id, counts[project.id] || 0, scope === project.id, projectColour(projectColourID(project.id, owner)), true)).join("")
    + menu
    + tile("all", "≡", "All", 0, scope === "all");
  if (focused) (row.querySelector(focused) || row.querySelector("select[data-scope-more]"))?.focus();
}

export function setScope(scope) {
  app.scope = scope;
  app.scopeArchived = (app.state.projects || []).some((project) => project.id === scope && project.archived);
  if (scope === "inbox" || scope === "all") {
    leaveProject();
    showView("conversations");
  } else openProject(scope);
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
  const project = (app.state.projects || []).find((entry) => entry.id === scope);
  if (project && (!project.archived || app.scopeArchived)) {
    app.scopeArchived = Boolean(project.archived);
    return;
  }
  app.scope = "all";
  if (app.selectedProject === scope) leaveProject();
  syncDecisionPane();
}

function closeTileMenu() {
  document.querySelector(".tile-menu")?.remove();
}

function openTileMenu(id, x, y) {
  closeTileMenu();
  const pinned = tileOrder().pinned.includes(id);
  const menu = document.createElement("div");
  menu.className = "project-menu-items tile-menu";
  menu.setAttribute("role", "menu");
  menu.dataset.pane = "float";
  menu.style.left = `${x}px`;
  menu.style.top = `${y}px`;
  menu.innerHTML = `<button type="button" role="menuitem" data-tile-change="pin">${pinned ? "Unpin" : "Pin to Front"}</button>`
    + `<button type="button" role="menuitem" data-tile-change="earlier">Move Earlier</button>`
    + `<button type="button" role="menuitem" data-tile-change="later">Move Later</button>`;
  menu.dataset.tileId = id;
  document.body.append(menu);
  menu.querySelector("button")?.focus();
}

function arrangeTile(id, change) {
  changeTiles({ ...change, id }, arranged);
  renderTiles();
  document.querySelector(`#project-tiles [data-tile="${CSS.escape(id)}"]`)?.focus();
}

export function bindTiles() {
  onTileOrderChange(renderTiles);
  document.addEventListener("click", (event) => {
    const chosen = event.target.closest("[data-tile-change]");
    if (chosen) {
      const id = chosen.closest(".tile-menu").dataset.tileId;
      closeTileMenu();
      const kind = chosen.dataset.tileChange;
      arrangeTile(id, kind === "pin" ? { kind: "pin", on: !tileOrder().pinned.includes(id) } : { kind: "step", by: kind === "earlier" ? -1 : 1 });
      return;
    }
    if (!event.target.closest(".tile-menu")) closeTileMenu();
  }, true);
  document.addEventListener("contextmenu", (event) => {
    const tile = event.target.closest("[data-tile]");
    if (!tile) return;
    event.preventDefault();
    openTileMenu(tile.dataset.tile, event.clientX, event.clientY);
  });
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && document.querySelector(".tile-menu")) {
      closeTileMenu();
      return;
    }
    const tile = event.target.closest?.("[data-tile]");
    if (!tile || !event.altKey || (event.key !== "ArrowLeft" && event.key !== "ArrowRight")) return;
    event.preventDefault();
    arrangeTile(tile.dataset.tile, { kind: "step", by: event.key === "ArrowLeft" ? -1 : 1 });
  });
  document.addEventListener("dragstart", (event) => {
    const tile = event.target.closest?.("[data-tile]");
    if (tile) event.dataTransfer.setData(tileType, tile.dataset.tile);
  });
  document.addEventListener("dragover", (event) => {
    if (event.target.closest?.("[data-tile]") && event.dataTransfer.types.includes(tileType)) event.preventDefault();
  });
  document.addEventListener("drop", (event) => {
    const target = event.target.closest?.("[data-tile]");
    const id = event.dataTransfer.getData(tileType);
    if (!target || !id || !arranged.includes(id)) return;
    event.preventDefault();
    arrangeTile(id, { kind: "move", target: target.dataset.tile });
  });
  document.addEventListener("click", (event) => {
    const button = event.target.closest("[data-scope]");
    if (button) setScope(button.dataset.scope);
    const open = event.target.closest("[data-open-decision]");
    if (open) openDecision(open.dataset.openDecision);
  });
  document.addEventListener("change", (event) => {
    if (event.target.matches("[data-scope-more]") && event.target.value) setScope(event.target.value);
  });
  const row = $("#project-tiles");
  if (row && typeof ResizeObserver !== "undefined") new ResizeObserver(() => { if (tileColumns(row.clientWidth) !== shownColumns) renderTiles(); }).observe(row);
}
