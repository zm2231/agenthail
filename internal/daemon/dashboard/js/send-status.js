import { $ } from "./dom.js";
import { app } from "./state.js";
import { resizeComposer, syncComposerAction } from "./composer.js";
import { isPendingSendStorage, retrySend, takeBackSend, unconfirmedSends, unconfirmedSendsOutside } from "./pending-send.js";

function joined(older, newer) {
  return newer.trim() ? `${older}\n\n${newer}` : older;
}

function listed(sessionId) {
  return app.selected?.id === sessionId || Boolean(app.state?.sessions?.some((session) => session.id === sessionId));
}

export function returnToComposer(item) {
  if (app.selected && (app.selected.id === item.sessionId || !listed(item.sessionId))) {
    const input = $("#message");
    input.value = joined(item.text, input.value);
    resizeComposer();
  } else {
    app.drafts.set(item.sessionId, joined(item.text, app.drafts.get(item.sessionId) || ""));
  }
}

function sendsToShow() {
  if (!app.selected) return [];
  return [...unconfirmedSends(app.selected.id), ...unconfirmedSendsOutside(listed)];
}

export function renderSendStatus() {
  const row = $("#send-status");
  if (!row) return;
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
