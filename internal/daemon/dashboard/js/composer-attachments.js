import { $ } from "./dom.js";
import { app } from "./state.js";
import { escape } from "./format.js";

const storageKey = "agenthail.heldAttachments";

let held = null;

function readStored() {
  try {
    const stored = JSON.parse(globalThis.localStorage.getItem(storageKey));
    return stored && typeof stored === "object" ? stored : {};
  } catch {
    return null;
  }
}

function current() {
  held ??= readStored() || {};
  return held;
}

function writeHeld(sessionId, attachments) {
  const next = { ...current(), ...(readStored() || {}) };
  if (attachments.length) next[sessionId] = attachments;
  else delete next[sessionId];
  held = next;
  try {
    if (Object.keys(next).length) globalThis.localStorage.setItem(storageKey, JSON.stringify(next));
    else globalThis.localStorage.removeItem(storageKey);
  } catch {}
}

export function heldAttachments(sessionId) {
  return current()[sessionId] || [];
}

export function sendableAttachments(sessionId) {
  return heldAttachments(sessionId).filter((attachment) => !attachment.missing);
}

export function holdAttachments(sessionId, attachments = []) {
  if (!attachments.length) return;
  const current = heldAttachments(sessionId);
  const known = new Set(current.map((attachment) => attachment.id));
  writeHeld(sessionId, [...attachments.filter((attachment) => !known.has(attachment.id)), ...current]);
  renderAttachments();
}

export function releaseAttachments(sessionId, sent = heldAttachments(sessionId)) {
  const gone = new Set(sent.map((attachment) => attachment.id));
  writeHeld(sessionId, heldAttachments(sessionId).filter((attachment) => !gone.has(attachment.id)));
  renderAttachments();
}

export function moveHeldAttachments(rekeyed) {
  for (const [retired, current] of Object.entries(rekeyed)) {
    const moving = heldAttachments(retired);
    if (!moving.length) continue;
    writeHeld(retired, []);
    holdAttachments(current, moving);
  }
}

export function renderAttachments() {
  const row = $("#composer-attachments");
  if (!row) return;
  const attachments = app.selected ? heldAttachments(app.selected.id) : [];
  row.hidden = attachments.length === 0;
  row.innerHTML = attachments
    .map((attachment) =>
      `<span class="composer-attachment${attachment.missing ? " is-missing" : ""}">${escape(attachment.name)}${attachment.missing ? " · no longer stored, attach it again" : ""}<button type="button" data-remove-attachment="${escape(attachment.id)}" aria-label="Remove ${escape(attachment.name)}">×</button></span>`)
    .join("");
}

export function bindAttachmentEvents(changed) {
  $("#composer-attachments")?.addEventListener("click", (event) => {
    const remove = event.target.closest?.("[data-remove-attachment]");
    if (!remove || !app.selected) return;
    releaseAttachments(app.selected.id, [{ id: remove.dataset.removeAttachment }]);
    changed();
  });
  globalThis.window?.addEventListener?.("storage", (event) => {
    if (event.key !== storageKey && event.key !== null) return;
    held = readStored() || held;
    changed();
  });
}
