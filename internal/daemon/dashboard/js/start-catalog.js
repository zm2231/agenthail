import { app } from "./state.js";
import { startHostQuery } from "./start-host.js";
import { responseError } from "./http.js";
import { loadStartRootUsage, renderStartConfigRoots } from "./start-roots.js";
import { renderStartSurfaceOptions } from "./start-form.js";
import { $ } from "./dom.js";
import { escape } from "./format.js";
import { friendlyError, toast } from "./feedback.js";

export async function loadStartOptions() {
  const host = app.startHost;
  const response = await fetch(`/api/v1/session-options${startHostQuery("?")}`);
  if (!response.ok) throw await responseError(response);
  const options = await response.json();
  if (host !== app.startHost) return;
  app.startOptions = options;
  app.launchers = options.launchers || [];
  renderStartConfigRoots(options.claudeConfigRoots || []);
  renderStartSurfaceOptions();
  loadStartRootUsage(options, host);
}

export async function loadStartModels() {
  const surfaceName = $("#new-conversation-surface").value;
  if (surfaceName !== "codex") return;
  const select = $("#new-conversation-model");
  const host = app.startHost;
  const cacheKey = `${host}/codex`;
  if (!app.startModels[cacheKey]) {
    const response = await fetch(`/api/models?surface=codex${startHostQuery("&")}`);
    if (!response.ok) throw await responseError(response);
    const payload = await response.json();
    app.startModels[cacheKey] = payload.models || [];
  }
  if (host !== app.startHost) return;
  const models = app.startModels[cacheKey];
  const current = select.value;
  select.innerHTML = [
    '<option value="">Use Codex default</option>',
    ...models.map(
      (model) => `<option value="${escape(model.id)}">${escape(model.displayName || model.id)}${model.default ? " (default)" : ""}</option>`,
    ),
  ].join("");
  if (models.some((model) => model.id === current)) select.value = current;
}

export function reloadStartCatalog() {
  app.startOptions = null;
  app.launchers = [];
  renderStartSurfaceOptions();
  loadStartOptions().then(() => loadStartModels()).catch((error) => toast(friendlyError(error)));
}
