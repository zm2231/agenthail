import { $ } from "./dom.js";
import { responseError } from "./http.js";
import { toast } from "./feedback.js";
import { load } from "./dashboard-state.js";
import { selectSession } from "./session-selection.js";
import { SharedEventSource } from "./shared-stream.js";

const state = { selected: "", view: null, head: "", fullscreen: false, history: null, patches: new Map() };

function describeChange(change) {
  const parts = (change.extensions ?? []).map((files) => {
    const count = (files.added?.length ?? 0) + (files.updated?.length ?? 0) + (files.deleted?.length ?? 0);
    if (files.created) return `created ${files.id}`;
    if (files.removed) return `removed ${files.id}`;
    return `changed ${count} ${count === 1 ? "file" : "files"} in ${files.id}`;
  });
  return parts.join(", ") || "no extension files";
}

async function readChanges(query) {
  const response = await fetch(`/api/v1/extensions/history?${new URLSearchParams(query)}`, { credentials: "same-origin" });
  if (!response.ok) throw await responseError(response);
  return (await response.json()).changes ?? [];
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

function button(text, onClick, className = "soft-button") {
  const node = element("button", { type: "button", className, textContent: text });
  node.addEventListener("click", onClick);
  return node;
}

function mountURL(mount, where = location) {
  return `${where.protocol}//${where.hostname}:${mount.port}${mount.entry}`;
}

function portOpens(target, where = location) {
  return target.port > 0 && (where.protocol !== "https:" || target.published);
}

function portState(target, where = location) {
  if (target.error) return "failed";
  if (!(target.port > 0)) return "no port";
  return portOpens(target, where) ? "open" : "not on tailnet";
}

function launcherRows(view, where = location) {
  const rows = [];
  for (const ext of view?.extensions ?? []) {
    const own = [];
    if (ext.page) {
      const status = ext.state === "loaded" ? portState(ext.page, where) : ext.state;
      own.push({ key: `page:${ext.id}`, ext, title: ext.app?.label || ext.label, detail: ext.id, badge: ext.badge ?? "", order: ext.app?.order ?? 0, status, src: status === "open" ? mountURL(ext.page, where) : "" });
    }
    for (const mount of ext.mounts ?? []) {
      const status = mount.state === "serving" ? portState(mount, where) : mount.state;
      own.push({ key: `mount:${ext.id}/${mount.name}`, ext, title: mount.app?.label || mount.label, detail: `${ext.label} · ${mount.name}`, badge: mount.badge ?? "", order: mount.app?.order ?? 0, status, src: status === "open" ? mountURL(mount, where) : "" });
    }
    if (!own.length) own.push({ key: `ext:${ext.id}`, ext, title: ext.label, detail: ext.jobs ? `${ext.id} · runs in the background` : ext.id, badge: ext.badge ?? "", order: ext.app?.order ?? 0, status: ext.state, src: "" });
    rows.push(...own);
  }
  return rows.sort((a, b) => a.order - b.order);
}

function statusClass(status) {
  if (status === "open" || status === "loaded") return "open";
  if (status === "pending") return "pending";
  if (status === "failed" || status === "error") return "dead";
  return "";
}

function problems(ext) {
  const lines = [];
  if (ext.error) lines.push(element("p", { className: "extension-error", textContent: ext.error }));
  if (ext.page?.error) lines.push(element("p", { className: "extension-error", textContent: `Page: ${ext.page.error}` }));
  const pending = [...(ext.pendingCapabilities ?? []), ...(ext.mounts ?? []).filter((mount) => mount.state === "pending").map((mount) => `${mount.label} (${mount.upstream})`)];
  if (pending.length) {
    lines.push(element("p", { className: "extension-notice-row" }, [element("span", { textContent: `Waits for your approval: ${pending.join(", ")}` }), button("Approve", () => act({ action: "approve", id: ext.id }))]));
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

function hasProblem(ext) {
  return Boolean(ext.error || ext.page?.error || ext.lastEventError || ext.pendingCapabilities?.length || (ext.mounts ?? []).some((mount) => mount.error || mount.state === "pending") || Object.values(ext.jobs ?? {}).some((job) => job.lastError));
}

function renderRail(rows) {
  const rail = $("#extensions-list");
  rail.replaceChildren();
  const view = state.view;
  if (!view) {
    rail.append(element("p", { className: "extensions-rail-note", textContent: "Reading extensions." }));
    return;
  }
  if (view.host !== "running") rail.append(element("p", { className: "extensions-rail-note extension-error", textContent: `The extension host is ${view.host}${view.hostError ? `: ${view.hostError}` : "."}` }));
  if (!rows.length) rail.append(element("p", { className: "extensions-rail-note", textContent: "No extensions yet. Each one is a folder in ~/.agenthail/extensions." }));
  for (const row of rows) {
    const marks = [element("span", { className: `status-pill ${statusClass(row.status)}`, textContent: row.status })];
    if (row.badge) marks.unshift(element("span", { className: "extension-badge", textContent: row.badge }));
    if (hasProblem(row.ext)) marks.unshift(element("span", { className: "extension-alert", title: "Needs attention", textContent: "!" }));
    const item = element("button", { type: "button", className: `extension-row${row.key === state.selected ? " active" : ""}` }, [
      element("span", { className: "extension-row-text" }, [element("strong", { textContent: row.title }), element("small", { textContent: row.detail })]),
      element("span", { className: "extension-row-marks" }, marks),
    ]);
    item.dataset.key = row.key;
    if (row.key === state.selected) item.setAttribute("aria-current", "page");
    item.addEventListener("click", () => {
      if (state.selected === row.key) return;
      state.selected = row.key;
      render();
    });
    rail.append(item);
  }
}

function renderStage(row) {
  const stage = $("#extensions-stage");
  const frame = $("#extensions-frame");
  stage.classList.toggle("fullscreen", state.fullscreen);
  stage.hidden = !row;
  const src = row?.src ?? "";
  if (frame.dataset.page !== src) {
    frame.dataset.page = src;
    frame.src = src || "about:blank";
  }
  frame.hidden = !src;
  if (!row) return;
  const tools = [];
  if (src) tools.push(element("a", { className: "soft-button", href: src, target: "_blank", rel: "noopener", textContent: "Open in new tab" }));
  tools.push(button(state.history ? "Hide history" : "History", toggleHistory));
  if (src) tools.push(button(state.fullscreen ? "Exit fullscreen" : "Fullscreen", () => setFullscreen(!state.fullscreen)));
  $("#extensions-toolbar").replaceChildren(
    element("div", { className: "extensions-toolbar-title" }, [element("h2", { textContent: row.title }), element("span", { className: `status-pill ${statusClass(row.status)}`, textContent: row.status })]),
    element("div", { className: "extensions-toolbar-actions" }, tools),
  );
  const notices = problems(row.ext);
  if (!src) notices.push(element("p", { className: "extension-quiet", textContent: emptyStage(row) }));
  $("#extensions-notices").replaceChildren(...notices);
  $("#extensions-notices").hidden = !notices.length;
  renderHistory();
}

function emptyStage(row) {
  if (row.key.startsWith("ext:")) return row.ext.description || "This extension has no page. Its jobs and actions run in the background.";
  if (row.status === "not on tailnet") return "This page is not published on the tailnet yet; it opens from this machine.";
  if (row.status === "no port") return "This page has no port yet.";
  return `This page is ${row.status}.`;
}

function setFullscreen(on) {
  state.fullscreen = on;
  render();
}

function toggleHistory() {
  const row = selectedRow();
  if (state.history || !row) {
    state.history = null;
    state.patches.clear();
    render();
    return;
  }
  state.history = { id: row.ext.id, changes: null };
  render();
  loadHistory();
}

async function loadHistory() {
  const history = state.history;
  if (!history) return;
  let changes;
  try {
    changes = await readChanges({ id: history.id, limit: 30 });
  } catch (error) {
    changes = error;
  }
  if (state.history?.id !== history.id) return;
  state.history.changes = changes;
  renderHistory();
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
  renderHistory();
}

async function changeAction(body) {
  try {
    await postExtensions(body);
  } catch (error) {
    toast(error.message);
  }
  await loadExtensions();
}

function renderHistory() {
  const drawer = $("#extensions-history");
  const history = state.history;
  drawer.hidden = !history;
  if (!history) return;
  const close = button("×", toggleHistory, "icon-button");
  close.setAttribute("aria-label", "Close history");
  const label = state.view?.extensions.find((ext) => ext.id === history.id)?.label ?? history.id;
  const items = [element("header", {}, [element("h2", { textContent: `History of ${label}` }), close])];
  if (history.changes === null) items.push(element("p", { className: "extension-quiet", textContent: "Reading changes." }));
  else if (history.changes instanceof Error) items.push(element("p", { className: "extension-error", textContent: history.changes.message }));
  else if (!history.changes.length) items.push(element("p", { className: "extension-quiet", textContent: "No changes recorded yet." }));
  else {
    for (const change of history.changes) {
      const buttons = [button(state.patches.has(change.rev) ? "Hide" : "Show", () => togglePatch(change.rev))];
      if (change.pending) buttons.push(button("Keep", () => changeAction({ action: "keep", rev: change.rev })));
      if (!change.root) buttons.push(button("Undo", () => changeAction({ action: "undo", rev: change.rev })));
      const when = new Date(change.time).toLocaleString();
      const card = element("section", { className: "extension-change" }, [
        element("p", { className: "extension-change-title", textContent: `${change.author}: ${change.message}` }),
        element("p", { className: "extension-quiet", textContent: `${describeChange(change)} · ${when}${change.pending ? " · waiting for you" : ""}` }),
        element("div", { className: "extension-change-actions" }, buttons),
      ]);
      if (state.patches.has(change.rev)) card.append(element("pre", { className: "extension-change-patch", textContent: state.patches.get(change.rev) }));
      items.push(card);
    }
  }
  drawer.replaceChildren(...items);
}

function selectedRow() {
  return launcherRows(state.view).find((row) => row.key === state.selected);
}

function render() {
  if (!$("#extensions-list")) return;
  const rows = launcherRows(state.view);
  if (!rows.some((row) => row.key === state.selected)) state.selected = (rows.find((row) => row.src) ?? rows[0])?.key ?? "";
  const row = rows.find((candidate) => candidate.key === state.selected);
  if (!row?.src) state.fullscreen = false;
  if (!row) state.history = null;
  else if (state.history && state.history.id !== row.ext.id) {
    state.history = { id: row.ext.id, changes: null };
    state.patches.clear();
    queueMicrotask(loadHistory);
  }
  renderRail(rows);
  renderStage(row);
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
  const [view, head] = await Promise.allSettled([readExtensions(), readChanges({ limit: 1 })]);
  state.view = view.status === "fulfilled" ? view.value : { host: "unavailable", hostError: view.reason.message, extensions: [] };
  state.head = head.status === "fulfilled" ? (head.value[0]?.rev ?? "") : state.head;
  render();
  await loadHistory();
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
  const frame = $("#extensions-frame");
  if (frame?.dataset.page && changed.includes(selectedRow()?.ext.id)) frame.src = frame.dataset.page;
}

async function build(form) {
  const submit = form.querySelector('button[type="submit"]');
  const values = Object.fromEntries(new FormData(form).entries());
  form.dataset.key ||= crypto.randomUUID();
  submit.disabled = true;
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
    $("#extensions-new-dialog").close();
    toast("The builder started. Each turn asks you to keep or undo what it changed.");
    await load(true);
    if (created.session?.id) await selectSession(created.session.id, true);
  } catch (error) {
    toast(error.message);
  } finally {
    submit.disabled = false;
  }
}

export function bindExtensions() {
  new SharedEventSource().addEventListener("extension.changed", extensionsChanged);
  $("#extensions-reload")?.addEventListener("click", () => act({ action: "reload" }));
  $("#extensions-new")?.addEventListener("click", () => {
    $("#extensions-new-dialog").showModal();
    $("#extensions-build-message").focus();
  });
  $("#extensions-new-close")?.addEventListener("click", () => $("#extensions-new-dialog").close());
  $("#extensions-build")?.addEventListener("input", (event) => delete event.currentTarget.dataset.key);
  $("#extensions-build")?.addEventListener("submit", (event) => {
    event.preventDefault();
    build(event.currentTarget);
  });
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && state.fullscreen && !$("#extensions-new-dialog")?.open) setFullscreen(false);
  });
}
