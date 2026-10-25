import { app } from "./state.js";
import { $ } from "./dom.js";
import { escape, labels } from "./format.js";
import { defaultStartHost, renderStartHost } from "./start-host.js";
import { clearLogicalRequest } from "./actions.js";
import { reloadStartCatalog } from "./start-catalog.js";

export function startable(name, connected) {
  return (connected || name === "claude") && ["codex", "claude", "notion"].includes(name);
}

export function startableSurfaces() {
  if (app.startOptions) return (app.startOptions.surfaces || []).filter((item) => startable(item.id, item.connected)).map((item) => item.id);
  return app.state.surfaces.filter((item) => startable(item.name, item.connected)).map((item) => item.name);
}

export function renderStartSurfaceOptions() {
  const select = $("#new-conversation-surface");
  const current = select.value;
  const surfaces = startableSurfaces();
  select.innerHTML = surfaces
    .map((name) => `<option value="${escape(name)}">${escape(labels[name] || name)}</option>`)
    .join("");
  if (surfaces.includes(current)) select.value = current;
  $("#new-conversation-toggle").hidden = app.state.surfaces.every((item) => !startable(item.name, item.connected));
  renderStartProjects();
  renderStartHost();
  syncStartForm();
}

export function renderStartProjects() {
  const select = $("#new-conversation-project");
  const folder = $("#new-conversation-form").querySelector('input[name="cwd"]');
  const projects = app.startOptions?.projects || [];
  select.innerHTML = projects
    .map((project) => `<option value="${escape(project.rootPath)}" title="${escape(project.rootPath)}">${escape(project.displayName)}</option>`)
    .join("") + '<option value="">Other folder</option>';
  const chosen = projects.find((project) => project.rootPath === folder.value) || (folder.value === "~" || folder.value === "" ? projects[0] : null);
  select.value = chosen ? chosen.rootPath : "";
  if (chosen) folder.value = chosen.rootPath;
}

export function syncStartForm() {
  const selected = $("#new-conversation-surface").value;
  const isCodex = selected === "codex";
  for (const [attribute, enabled] of [["data-code-start", selected !== "notion"], ["data-claude-start", selected === "claude"]]) {
    document.querySelectorAll(`[${attribute}]`).forEach(field => { field.hidden = !enabled; field.querySelectorAll("input,select,textarea").forEach(input => input.disabled = !enabled); });
  }
  $("[data-start-folder]").hidden = selected === "notion" || $("#new-conversation-project").value !== "";
  const configRoot = $("[data-claude-root]");
  if (configRoot.dataset.single === "true") configRoot.hidden = true;
  document.querySelectorAll("[data-codex-start]").forEach((field) => {
    field.hidden = !isCodex;
    field.querySelectorAll("input,select,textarea").forEach((input) => {
      input.disabled = !isCodex;
    });
  });
  renderStartLaunchers();
}

export function renderStartLaunchers() {
  const select = $("#new-conversation-launcher");
  const current = select.value;
  const agent = $("#new-conversation-surface").value;
  const options = app.launchers.filter(item => item.agents.includes(agent));
  select.innerHTML = '<option value="">Use default</option>' + options.map(item =>
    `<option value="${escape(item.id)}"${item.available ? "" : " disabled"}>${escape(item.label)}${item.available ? "" : " (unavailable)"}</option>`,
  ).join("");
  select.value = options.some(item => item.id === current && item.available) ? current : "";
  const launcher = options.find(item => item.id === select.value);
  $("#new-conversation-launcher-detail").textContent = launcher?.detail || "";
  if (launcher && !launcher.sessionOptions) {
    document.querySelectorAll("[data-launch-option]").forEach(field => { field.hidden = true; field.querySelectorAll("input,select,textarea").forEach(input => input.disabled = true); });
  }
}

export function toggleNewConversationForm(show, place) {
  const form = $("#new-conversation-form");
  form.hidden = !show;
  if (!show) {
    clearLogicalRequest("session-create");
    return;
  }
  clearLogicalRequest("session-create");
  app.startHost = place ? place.host : defaultStartHost();
  if (place) form.querySelector('input[name="cwd"]').value = place.folder;
  $("#new-conversation-host-menu").hidden = true;
  reloadStartCatalog();
  form.querySelector('input[name="alias"]').focus();
}
