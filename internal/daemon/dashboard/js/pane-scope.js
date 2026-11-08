import { app } from "./state.js";
import { $ } from "./dom.js";

export function scopeKeeps(scope, projectId) {
  return !scope || scope === "all" || scope === "inbox" || projectId === scope;
}

export function scopeAfterSelecting(scope, session) {
  if (scope === "inbox") return "all";
  if (scope && scope !== "all" && session?.project?.id !== scope) return "all";
  return scope || "all";
}

export function syncDecisionPane() {
  const pane = $("#decision-pane");
  if (!pane) return;
  const shows = app.scope === "inbox";
  pane.hidden = !shows;
  const notebook = document.querySelector(".notebook:not(.project-pane):not(.decision-pane)");
  if (shows) {
    if (notebook) notebook.hidden = true;
    const project = $("#project-pane");
    if (project) project.hidden = true;
  } else if (notebook && !app.selectedProject) {
    notebook.hidden = false;
  }
}
