import { $ } from "./dom.js";
import { syncStartForm, toggleNewConversationForm } from "./start-form.js";
import { loadStartModels, reloadStartCatalog } from "./start-catalog.js";
import { friendlyError, toast } from "./feedback.js";
import { renderStartHost, startHosts } from "./start-host.js";
import { app } from "./state.js";
import { action, clearLogicalRequest, logicalAction } from "./actions.js";
import { load } from "./dashboard-state.js";
import { creationFailureMessage, creationNotice } from "./delivery-labels.js";
import { selectSession } from "./session-selection.js";
import { syncStartRootDetail } from "./start-roots.js";

export function bindStartEvents() {
  $("#new-conversation-toggle").addEventListener("click", () => toggleNewConversationForm(true));
  $("#new-conversation-close").addEventListener("click", () => toggleNewConversationForm(false));
  $("#new-conversation-surface").addEventListener("change", () => {
    syncStartForm();
    loadStartModels().catch((error) => toast(friendlyError(error)));
  });
  $("#new-conversation-launcher").addEventListener("change", syncStartForm);
  $("#new-conversation-config-root").addEventListener("change", () => syncStartRootDetail());
  $("#new-conversation-project").addEventListener("change", (event) => {
    const folder = $("#new-conversation-form").querySelector('input[name="cwd"]');
    folder.value = event.target.value || "~";
    syncStartForm();
    if (!event.target.value) folder.focus();
  });
  $("#new-conversation-host").addEventListener("click", () => {
    const menu = $("#new-conversation-host-menu");
    menu.hidden = !menu.hidden;
    renderStartHost();
  });
  $("#new-conversation-host-menu").addEventListener("click", (event) => {
    const option = event.target.closest("[data-start-host]");
    if (!option || option.disabled) return;
    $("#new-conversation-host-menu").hidden = true;
    if (option.dataset.startHost === app.startHost) {
      renderStartHost();
      return;
    }
    app.startHost = option.dataset.startHost;
    const form = $("#new-conversation-form");
    for (const name of ["launcher", "model", "effort", "cwd"]) form.querySelectorAll(`[name="${name}"]`).forEach((field) => { field.value = name === "cwd" ? "~" : ""; });
    clearLogicalRequest("session-create");
    reloadStartCatalog();
  });
  $("#new-conversation-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    const form = event.currentTarget;
    const button = form.querySelector('button[type="submit"]');
    const values = Object.fromEntries(new FormData(form).entries());
    if (app.startHost) {
      const host = startHosts().find((item) => item.id === app.startHost);
      if (host?.state !== "connected") {
        toast(`${host?.name || "That host"} is not connected.`);
        return;
      }
      values.host = app.startHost;
    }
    button.disabled = true;
    try {
      if (values.outputSchema?.trim()) values.outputSchema = JSON.parse(values.outputSchema); else delete values.outputSchema;
      const createAction = values.surface === "notion" ? "notion-create" : "session-create";
      const response = createAction === "session-create"
        ? await logicalAction("session-create", createAction, values)
        : await action(createAction, values, crypto.randomUUID(), "");
      form.reset();
      toggleNewConversationForm(false);
      await load(true);
      toast(creationNotice(response, values));
      const sessionID = response.sessionId || response.session?.id;
      if (sessionID) await selectSession(sessionID, true);
    } catch (error) {
      toast(creationFailureMessage(error));
    } finally {
      button.disabled = false;
    }
  });
}
