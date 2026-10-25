import { app } from "./state.js";
import { $ } from "./dom.js";
import { escape } from "./format.js";

export function sharedHolders(session) {
  return session?.surface === "claude" && (session.holders || []).length > 1 ? session.holders : [];
}

export function holderLabel(holder) {
  return [`pid ${holder.pid}`, holder.tty, holder.status].filter(Boolean).join(", ");
}

export function renderHolderPicker() {
  const holders = sharedHolders(app.selected);
  const picker = $("#holder-picker");
  const select = $("#holder-pid");
  picker.hidden = holders.length === 0;
  if (holders.length === 0) {
    select.innerHTML = "";
    return;
  }
  const chosen = select.dataset.session === app.selected.id ? select.value : "";
  $("#holder-summary").textContent = `Open in ${holders.length} terminals. Send to`;
  select.innerHTML = `<option value="">Choose a terminal</option>${holders.map((holder) => `<option value="${escape(String(holder.pid))}">${escape(holderLabel(holder))}</option>`).join("")}`;
  select.value = holders.some((holder) => String(holder.pid) === chosen) ? chosen : "";
  select.dataset.session = app.selected.id;
}

export function chosenHolder(sessionID) {
  const select = $("#holder-pid");
  if (sessionID !== app.selected?.id || sharedHolders(app.selected).length === 0 || select.dataset.session !== sessionID) return 0;
  return Number(select.value) || 0;
}

export function withHolder(sessionID, payload) {
  const pid = chosenHolder(sessionID);
  if (!pid) return payload;
  const holder = sharedHolders(app.selected).find((candidate) => candidate.pid === pid);
  const startedAt = Date.parse(holder?.startedAt || "");
  return Number.isFinite(startedAt) ? { ...payload, pid, pidStartedAt: startedAt } : { ...payload, pid };
}
