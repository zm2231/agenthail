import { $ } from "./dom.js";
import { responseError } from "./http.js";
import { toast } from "./feedback.js";
import { load } from "./dashboard-state.js";
import { selectSession } from "./session-selection.js";
import { SharedEventSource } from "./shared-stream.js";

const state = { selected: "", view: null, changes: null, head: "", patches: new Map() };

export function describeChange(change) {
  const parts = (change.extensions ?? []).map((files) => {
    const count = (files.added?.length ?? 0) + (files.updated?.length ?? 0) + (files.deleted?.length ?? 0);
    if (files.created) return `created ${files.id}`;
    if (files.removed) return `removed ${files.id}`;
    return `changed ${count} ${count === 1 ? "file" : "files"} in ${files.id}`;
  });
  return parts.join(", ") || "no extension files";
}

async function readChanges() {
  const response = await fetch("/api/v1/extensions/history?limit=30", { credentials: "same-origin" });
  if (!response.ok) throw await responseError(response);
  return (await response.json()).changes ?? [];
}

async function changeAction(body) {
  try {
    await postExtensions(body);
  } catch (error) {
    toast(error.message);
  }
  await loadExtensions();
}

async function togglePatch(rev) {
  if (state.patches.has(rev)) {
    state.patches.delete(rev);
  } else {
    const response = await fetch(`/api/v1/extensions/history/${encodeURIComponent(rev)}`, { credentials: "same-origin" });
    if (!response.ok) {
      toast((await responseError(response)).message);
      return;
    }
    const shown = await response.json();
    state.patches.set(rev, shown.patch + (shown.truncated ? "\n[cut: the change is larger than this view]" : ""));
  }
  renderChanges();
}

function renderChanges() {
  const list = $("#extensions-changes");
  if (!list) return;
  list.replaceChildren(element("h2", { textContent: "Changes" }));
  if (state.changes === null) return;
  if (!state.changes.length) {
    list.append(element("div", { className: "empty-card", textContent: "No changes recorded yet." }));
    return;
  }
  state.changes.forEach((change) => {
    const buttons = [];
    const show = element("button", { type: "button", className: "soft-button", textContent: state.patches.has(change.rev) ? "Hide" : "Show" });
    show.addEventListener("click", () => togglePatch(change.rev));
    buttons.push(show);
    if (change.pending) {
      const keep = element("button", { type: "button", className: "soft-button", textContent: "Keep" });
      keep.addEventListener("click", () => changeAction({ action: "keep", rev: change.rev }));
      buttons.push(keep);
    }
    if (!change.root) {
      const undo = element("button", { type: "button", className: "soft-button", textContent: "Undo" });
      undo.addEventListener("click", () => changeAction({ action: "undo", rev: change.rev }));
      buttons.push(undo);
    }
    const when = new Date(change.time).toLocaleString();
    const card = element("section", { className: "settings-card extension-change" }, [
      element("p", { className: "extension-title", textContent: `${change.author}: ${change.message}` }),
      element("p", { textContent: `${describeChange(change)} · ${when}${change.pending ? " · waiting for you" : ""}` }),
      element("div", { className: "extension-change-actions" }, buttons),
    ]);
    if (state.patches.has(change.rev)) card.append(element("pre", { className: "extension-change-patch", textContent: state.patches.get(change.rev) }));
    list.append(card);
  });
}

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
  const [view, changes] = await Promise.allSettled([readExtensions(), readChanges()]);
  state.view = view.status === "fulfilled" ? view.value : { host: "unavailable", hostError: view.reason.message, extensions: [] };
  state.changes = changes.status === "fulfilled" ? changes.value : [];
  state.head = state.changes[0]?.rev ?? "";
  render();
  renderChanges();
}

// The shared stream replays recent events when it connects, so a frame is
// reloaded only when the folder's newest change actually moved.
async function extensionsChanged(event) {
  if ($("#extensions-view")?.classList.contains("hidden")) return;
  let changed = [];
  try {
    changed = JSON.parse(event.data).data?.extensions ?? [];
  } catch {
    changed = [];
  }
  const before = state.head;
  await loadExtensions();
  if (!before || state.head === before) return;
  const selected = state.selected.replace(/^(page|mount):/, "").split("/")[0];
  const frame = $("#extensions-frame");
  if (frame?.dataset.page && changed.includes(selected)) frame.src = frame.dataset.page;
}

async function build(form) {
  const button = form.querySelector('button[type="submit"]');
  const values = Object.fromEntries(new FormData(form).entries());
  form.dataset.key ||= crypto.randomUUID();
  button.disabled = true;
  try {
    const response = await fetch("/api/v1/extensions/build", {
      method: "POST",
      credentials: "same-origin",
      headers: { "content-type": "application/json", "Idempotency-Key": form.dataset.key },
      body: JSON.stringify(values),
    });
    if (!response.ok) throw await responseError(response);
    const created = await response.json();
    delete form.dataset.key;
    form.reset();
    toast("The builder started. Each turn asks you to keep or undo what it changed.");
    await load(true);
    if (created.session?.id) await selectSession(created.session.id, true);
  } catch (error) {
    toast(error.message);
  } finally {
    button.disabled = false;
  }
}

export function bindExtensions() {
  new SharedEventSource().addEventListener("extension.changed", extensionsChanged);
  $("#extensions-reload")?.addEventListener("click", () => act({ action: "reload" }));
  $("#extensions-build")?.addEventListener("input", (event) => delete event.currentTarget.dataset.key);
  $("#extensions-build")?.addEventListener("submit", (event) => {
    event.preventDefault();
    build(event.currentTarget);
  });
}
