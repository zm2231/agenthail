import { $ } from "./dom.js";
import { app } from "./state.js";
import { resizeComposer, syncComposerAction } from "./composer.js";
import { isPendingSendStorage, retrySend, takeBackSend, unconfirmedSends } from "./pending-send.js";

function joined(older, newer) {
  return newer.trim() ? `${older}\n\n${newer}` : older;
}

export function returnToComposer(item) {
  if (app.selected?.id === item.sessionId) {
    const input = $("#message");
    input.value = joined(item.text, input.value);
    resizeComposer();
  } else {
    app.drafts.set(item.sessionId, joined(item.text, app.drafts.get(item.sessionId) || ""));
  }
}

function oldestUnconfirmed() {
  return app.selected ? unconfirmedSends(app.selected.id)[0] : undefined;
}

export function renderSendStatus() {
  const row = $("#send-status");
  if (!row) return;
  const sends = app.selected ? unconfirmedSends(app.selected.id) : [];
  row.hidden = sends.length === 0;
  if (!sends.length) return;
  const more = sends.length > 1 ? ` and ${sends.length - 1} more` : "";
  $("#send-status-text").textContent = `${sends[0].retrying ? "Not confirmed yet · retrying" : "Not confirmed. It may already have arrived."} “${sends[0].text}”${more}`;
}

export function bindSendStatus() {
  $("#send-status-retry").addEventListener("click", () => {
    const item = oldestUnconfirmed();
    if (item) retrySend(item.id);
  });
  $("#send-status-put-back").addEventListener("click", () => {
    const item = oldestUnconfirmed();
    if (!item || !window.confirm("It may already have arrived. Put it back in the composer?")) return;
    const taken = takeBackSend(item.id);
    if (taken) returnToComposer(taken);
    syncComposerAction();
  });
  window.addEventListener("storage", (event) => {
    if (isPendingSendStorage(event.key)) renderSendStatus();
  });
}
