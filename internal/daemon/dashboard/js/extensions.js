import { $ } from "./dom.js";
import { responseError } from "./http.js";

const state = { selected: "", view: null };

async function readExtensions() {
  const response = await fetch("/api/v1/extensions", { credentials: "same-origin" });
  if (!response.ok) throw await responseError(response);
  return response.json();
}

async function postExtensions(body) {
  const response = await fetch("/api/v1/extensions", {
    method: "POST",
    credentials: "same-origin",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!response.ok) throw await responseError(response);
  return response.json();
}

function element(tag, props = {}, children = []) {
  const node = document.createElement(tag);
  Object.assign(node, props);
  for (const child of children) node.append(child);
  return node;
}

function describe(ext) {
  const lines = [];
  if (ext.error) lines.push(element("p", { className: "extension-error", textContent: ext.error }));
  if (ext.pendingCapabilities?.length) {
    const approve = element("button", { className: "soft-button", type: "button", textContent: `Approve ${ext.pendingCapabilities.join(", ")}` });
    approve.addEventListener("click", () => act({ action: "approve", id: ext.id }));
    lines.push(element("p", { textContent: "Waits for your approval to act: " }), approve);
  }
  for (const [name, job] of Object.entries(ext.jobs ?? {})) {
    if (job.lastError) lines.push(element("p", { className: "extension-error", textContent: `Job ${name} failed: ${job.lastError}` }));
  }
  if (ext.lastEventError) lines.push(element("p", { className: "extension-error", textContent: `Event handler failed: ${ext.lastEventError}` }));
  return lines;
}

function render() {
  const list = $("#extensions-list");
  const frame = $("#extensions-frame");
  if (!list || !frame) return;
  const view = state.view;
  list.replaceChildren();
  if (!view) {
    list.append(element("div", { className: "empty-card", textContent: "Reading extensions." }));
    return;
  }
  if (view.host !== "running") {
    list.append(element("div", { className: "empty-card", textContent: `The extension host is ${view.host}${view.hostError ? `: ${view.hostError}` : "."}` }));
  }
  if (!view.extensions.length) {
    list.append(element("div", { className: "empty-card", textContent: "No extensions yet. Each one is a folder in ~/.agenthail/extensions." }));
  }
  if (!view.extensions.some((ext) => ext.id === state.selected && ext.page)) {
    state.selected = view.extensions.find((ext) => ext.page && ext.state === "loaded")?.id ?? "";
  }
  for (const ext of view.extensions) {
    const title = element("button", { type: "button", className: `extension-title${ext.id === state.selected ? " active" : ""}`, disabled: !ext.page, textContent: ext.label });
    title.addEventListener("click", () => {
      state.selected = ext.id;
      render();
    });
    const pill = element("span", { className: `status-pill ${ext.state === "loaded" ? "open" : "dead"}`, textContent: ext.state });
    list.append(element("section", { className: "settings-card extension-card" }, [element("div", { className: "extension-heading" }, [title, pill]), ...describe(ext)]));
  }
  const page = view.extensions.find((ext) => ext.id === state.selected)?.page ?? "";
  if (frame.dataset.page !== page) {
    frame.dataset.page = page;
    frame.src = page || "about:blank";
  }
  frame.hidden = !page;
}

async function act(body) {
  try {
    state.view = await postExtensions(body);
  } catch (error) {
    state.view = { host: "unavailable", hostError: error.message, extensions: state.view?.extensions ?? [] };
  }
  render();
}

export async function loadExtensions() {
  try {
    state.view = await readExtensions();
  } catch (error) {
    state.view = { host: "unavailable", hostError: error.message, extensions: [] };
  }
  render();
}

export function bindExtensions() {
  $("#extensions-reload")?.addEventListener("click", () => act({ action: "reload" }));
}
