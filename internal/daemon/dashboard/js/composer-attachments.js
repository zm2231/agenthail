import { $ } from "./dom.js";
import { app } from "./state.js";
import { escape } from "./format.js";
import { toast } from "./feedback.js";

const storagePrefix = "agenthail.heldAttachment.";
const unsaved = new Map();
let order = 0;

function keyFor(sessionId, attachmentId) {
  return storagePrefix + JSON.stringify([sessionId, attachmentId]);
}

function records() {
  const found = new Map(unsaved);
  try {
    const storage = globalThis.localStorage;
    for (let index = 0; index < storage.length; index++) {
      const key = storage.key(index);
      if (!key?.startsWith(storagePrefix)) continue;
      try {
        const record = JSON.parse(storage.getItem(key));
        if (record?.sessionId && record.attachment?.id) found.set(key, record);
      } catch {}
    }
  } catch {}
  return [...found.values()];
}

function save(record) {
  const key = keyFor(record.sessionId, record.attachment.id);
  try {
    globalThis.localStorage.setItem(key, JSON.stringify(record));
    unsaved.delete(key);
    return true;
  } catch {
    unsaved.set(key, record);
    return false;
  }
}

function drop(sessionId, attachmentId) {
  const key = keyFor(sessionId, attachmentId);
  unsaved.delete(key);
  try {
    globalThis.localStorage.removeItem(key);
  } catch {}
}

export function heldAttachments(sessionId) {
  return records()
    .filter((record) => record.sessionId === sessionId)
    .sort((a, b) => a.heldAt - b.heldAt || a.order - b.order)
    .map((record) => record.attachment);
}

export function sendableAttachments(sessionId) {
  return heldAttachments(sessionId).filter((attachment) => !attachment.missing);
}

export function attachmentSteerRefusal(attachments, capabilities) {
  if (!attachments.length) return "";
  if (attachments.some((attachment) => attachment.missing)) return "An attached file is gone. Remove it to steer.";
  if (capabilities?.steerImages === undefined) return "Update Agenthail on this computer to steer attachments. Send queues them for next.";
  const images = attachments.some((attachment) => ["image/png", "image/jpeg", "image/gif"].includes(attachment.mediaType));
  if (!capabilities.steerImages && images) return "Attached images can't join a running turn on this session. Send queues them for next.";
  return "";
}

export function holdAttachments(sessionId, attachments = [], { warn = true } = {}) {
  const known = new Set(heldAttachments(sessionId).map((attachment) => attachment.id));
  const heldAt = Date.now();
  let saved = true;
  for (const attachment of attachments) {
    if (!known.has(attachment.id)) saved = save({ sessionId, attachment, heldAt, order: ++order }) && saved;
  }
  renderAttachments();
  if (!saved && warn) toast("This browser couldn't save the attached files. They stay in this tab only; send them before you reload.");
  return saved;
}

export function releaseAttachments(sessionId, sent = heldAttachments(sessionId)) {
  for (const attachment of sent) drop(sessionId, attachment.id);
  renderAttachments();
}

export function moveHeldAttachments(rekeyed) {
  let stranded = false;
  for (const record of records()) {
    const current = rekeyed[record.sessionId];
    if (!current || current === record.sessionId) continue;
    if (save({ ...record, sessionId: current })) drop(record.sessionId, record.attachment.id);
    else stranded = true;
  }
  if (stranded) toast("This browser couldn't save the attached files for the renamed conversation. They stay in this tab only; send them before you reload.");
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
    if (event.key === null || event.key?.startsWith(storagePrefix)) changed();
  });
}
