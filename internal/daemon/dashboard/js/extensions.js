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

export function mountURL(mount, where = location) {
  return `${where.protocol}//${where.hostname}:${mount.port}${mount.entry}`;
}

function mountOpens(mount, where = location) {
  return mount.state === "serving" && mount.port > 0 && (where.protocol !== "https:" || mount.published);
}

function frames(ext) {
  const targets = [];
  if (ext.page && ext.state === "loaded") targets.push({ key: `page:${ext.id}`, label: ext.label, src: ext.page });
  for (const mount of ext.mounts ?? []) {
    if (mountOpens(mount)) targets.push({ key: `mount:${ext.id}/${mount.name}`, label: mount.label, src: mountURL(mount) });
  }
  return targets;
}

function describe(ext) {
  const lines = [];
  if (ext.error) lines.push(element("p", { className: "extension-error", textContent: ext.error }));
  const pending = [...(ext.pendingCapabilities ?? []), ...(ext.mounts ?? []).filter((mount) => mount.state === "pending").map((mount) => `${mount.label} (${mount.upstream})`)];
  if (pending.length) {
    const approve = element("button", { className: "soft-button", type: "button", textContent: `Approve ${pending.join(", ")}` });
    approve.addEventListener("click", () => act({ action: "approve", id: ext.id }));
    lines.push(element("p", { textContent: "Waits for your approval: " }), approve);
  }
  for (const mount of ext.mounts ?? []) {
    if (mount.error) lines.push(element("p", { className: "extension-error", textContent: `${mount.label}: ${mount.error}` }));
    else if (mount.signin && !mount.key && mount.state === "serving") lines.push(element("p", { textContent: `${mount.label} signs in once you name its key: agenthail extensions key ${ext.id} ${mount.name} --key-file <path>` }));
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
  const targets = view.extensions.flatMap(frames);
  if (!targets.some((target) => target.key === state.selected)) state.selected = targets[0]?.key ?? "";
  for (const ext of view.extensions) {
    const opens = frames(ext);
    const buttons = opens.map((target) => {
      const button = element("button", { type: "button", className: `extension-title${target.key === state.selected ? " active" : ""}`, textContent: target.label });
      button.addEventListener("click", () => {
        state.selected = target.key;
        render();
      });
      return button;
    });
    if (!buttons.length) buttons.push(element("span", { className: "extension-title", textContent: ext.label }));
    const pill = element("span", { className: `status-pill ${ext.state === "loaded" ? "open" : "dead"}`, textContent: ext.state });
    list.append(element("section", { className: "settings-card extension-card" }, [element("div", { className: "extension-heading" }, [...buttons, pill]), ...describe(ext)]));
  }
  const page = targets.find((target) => target.key === state.selected)?.src ?? "";
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
