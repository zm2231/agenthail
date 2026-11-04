import { $ } from "./dom.js";
import { app } from "./state.js";
import { resizeComposer, syncComposerAction } from "./composer.js";
import { toast } from "./feedback.js";
import { holdAttachments } from "./composer-attachments.js";
import { isPendingSendStorage, keepSend, retrySend, takeBackSend, unconfirmedSends, unconfirmedSendsOutside } from "./pending-send.js";

function joined(older, newer) {
  if (!older.trim()) return newer;
  return newer.trim() ? `${older}\n\n${newer}` : older;
}

function listed(sessionId) {
  if (!app.state?.sessions || app.selected?.id === sessionId) return true;
  const isIt = (session) => session.id === sessionId;
  return app.state.sessions.some(isIt) || Boolean(app.codexSearch?.results?.some(isIt));
}

export function returnToSession(sessionId, text, attachments = []) {
  if (app.selected?.id === sessionId) {
    const input = $("#message");
    input.value = joined(text, input.value);
    resizeComposer();
  } else {
    app.drafts.set(sessionId, joined(text, app.drafts.get(sessionId) || ""));
  }
  holdAttachments(sessionId, attachments);
}

export function returnToComposer(item) {
  const attached = Boolean(item.attachments?.length);
  if (app.selected?.id === item.sessionId || listed(item.sessionId)) {
    returnToSession(item.sessionId, item.text, item.attachments);
  } else if (app.selected && !attached) {
    returnToSession(app.selected.id, item.text);
  } else {
    keepSend(item);
  }
}

function sendsToShow() {
  if (!app.selected) return [];
  return [...unconfirmedSends(app.selected.id), ...unconfirmedSendsOutside(listed)];
}

let toldAboutUnlisted = false;

function pointToUnlistedSends() {
  if (toldAboutUnlisted || app.selected || !unconfirmedSendsOutside(listed).length) return;
  toldAboutUnlisted = true;
  toast("A message wasn't confirmed. Open any conversation to see it.");
}

export function renderSendStatus() {
  const row = $("#send-status");
  if (!row) return;
  pointToUnlistedSends();
  const sends = sendsToShow();
  row.hidden = sends.length === 0;
  if (!sends.length) return;
  const [oldest] = sends;
  const state = oldest.retrying ? "Not confirmed yet · retrying" : "Not confirmed. It may already have arrived.";
  const elsewhere = listed(oldest.sessionId) ? "" : " Its conversation is no longer listed.";
  const more = sends.length > 1 ? ` and ${sends.length - 1} more` : "";
  $("#send-status-text").textContent = `${state}${elsewhere} “${oldest.text}”${more}`;
}

export function bindSendStatus() {
  $("#send-status-retry").addEventListener("click", () => {
    const [item] = sendsToShow();
    if (item) retrySend(item.id);
  });
  $("#send-status-put-back").addEventListener("click", () => {
    const [item] = sendsToShow();
    if (!item || !window.confirm("It may already have arrived. Put it back in the composer?")) return;
    const taken = takeBackSend(item.id);
    if (taken) returnToComposer(taken);
    syncComposerAction();
  });
  window.addEventListener("storage", (event) => {
    if (isPendingSendStorage(event.key)) renderSendStatus();
  });
}
