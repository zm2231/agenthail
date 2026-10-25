import { $ } from "./dom.js";
import { app } from "./state.js";
import { friendlyError, toast } from "./feedback.js";
import { action } from "./actions.js";
import { load } from "./dashboard-state.js";

export function syncSessionOperationFields() {
  const form = $("#session-operation-form");
  const surface = app.selected?.surface;
  const lifecycle = app.selected?.capabilities?.lifecycle || {};
  $("#turn-effort").closest("details").hidden = surface !== "codex";
  let offered = false;
  for (const option of form.elements.operation.options) {
    const lifecycleOperation = option.value.startsWith("session-lifecycle-") ? option.value.slice("session-lifecycle-".length) : "";
    const matches = surface === "claude" ? Boolean(lifecycleOperation && lifecycle[lifecycleOperation]) : !lifecycleOperation;
    offered ||= matches;
    option.hidden = !matches;
    option.disabled = !matches || (Boolean(app.selected?.readOnly) && option.value !== "native-list");
  }
  form.closest("details").hidden = surface === "claude" ? !offered : surface !== "codex";
  const chosen = form.elements.operation.selectedOptions[0];
  if (!chosen || chosen.disabled) {
    form.elements.operation.value = [...form.elements.operation.options].find(option => !option.disabled)?.value || "";
  }
  const operation = form.elements.operation.value;
  const allowed = ({"native-list":["cursor"],"native-add":["message","clientId"],"native-update":["message","id"],"native-delete":["id"],"native-reorder":["ids"],"native-start":["id"]}[operation] || []);
  for (const field of form.querySelectorAll("input,textarea")) { field.closest("label").hidden = !allowed.includes(field.name); field.disabled = !allowed.includes(field.name); }
  if (operation === "native-add" && !form.elements.clientId.value) form.elements.clientId.value = crypto.randomUUID();
}

export function bindSessionOperationEvents() {
  $("#session-operation-form").addEventListener("submit", async event => {
    event.preventDefault();
    const form = event.currentTarget;
    const button = form.querySelector("button");
    if (!app.selected) return toast("Select a conversation first.");
    const values = Object.fromEntries(new FormData(event.currentTarget));
    button.disabled = true;
    try {
      const isQueue = values.operation.startsWith("native-");
      const payload = isQueue ? {nativeQueue: {queueAction: values.operation.slice(7), message: values.message, queuedSubmissionId: values.id, clientUserMessageId: values.clientId, queuedSubmissionIds: values.ids ? values.ids.split(",").map(x => x.trim()) : [], cursor: values.cursor}} : {};
      const result = await action(isQueue ? "native-queue" : values.operation, payload);
      $("#session-operation-result").textContent = JSON.stringify(result.result, null, 2);
      if (values.operation === "native-add") {
        form.elements.clientId.value = crypto.randomUUID();
      }
      await load(true);
    } catch (error) { toast(friendlyError(error)); }
    finally { button.disabled = false; }
  });
  $("#session-operation-form").elements.operation.addEventListener("change", syncSessionOperationFields);
  syncSessionOperationFields();
}
