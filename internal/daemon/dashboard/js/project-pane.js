import { app } from "./state.js";
import { $ } from "./dom.js";
import { renderProjectView } from "./project-view.js";
import { showView, syncConversationLayout, mobileConversationView } from "./navigation.js";

export function shownProject() {
  return app.selectedProject ? (app.state.projects || []).find((project) => project.id === app.selectedProject) || null : null;
}

export function syncProjectPane() {
  const pane = $("#project-pane");
  if (!pane) return;
  const project = shownProject();
  pane.hidden = !project;
  document.querySelector(".notebook:not(.project-pane)").hidden = Boolean(project);
  pane.innerHTML = project ? renderProjectView(project) : "";
}

let ticking = 0;

export function openProject(id) {
  if (!ticking) ticking = setInterval(() => { if (app.selectedProject) syncProjectPane(); }, 60_000);
  showView("conversations");
  app.selectedProject = id;
  app.projectEarlier = null;
  app.mobileChatOpen = mobileConversationView();
  syncConversationLayout();
  syncProjectPane();
}

export function leaveProject() {
  if (!app.selectedProject) return;
  app.selectedProject = null;
  app.projectEarlier = null;
  syncProjectPane();
}
