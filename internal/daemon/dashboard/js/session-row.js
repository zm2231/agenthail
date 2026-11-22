import { holderLabel, sharedHolders } from "./holders.js";
import { livePlan, planDetail, presenceLabel, presenceTone } from "./presence.js";
import { app } from "./state.js";
import { escape, labels } from "./format.js";
import { displayName, rawDisplayName } from "./session-labels.js";
import { hostBadge } from "./hosts.js";
import { unread } from "./read-state.js";

export function sessionButton(session) {
  const holders = sharedHolders(session);
  const plan = livePlan(session);
  return `<button class="session ${app.selected?.id === session.id ? "selected" : ""}${session.archived ? " archived" : ""}${unread(session) ? " unread" : ""}" title="${escape(rawDisplayName(session))}" type="button" data-session="${escape(session.id)}"><div class="session-name"><i class="dot ${escape(presenceTone(session))}"></i><span>${escape(displayName(session))}</span>${unread(session) ? '<span class="unread-mark">Unread</span>' : ""}</div><div class="session-detail">${hostBadge(session)}<span>${escape(labels[session.surface] || session.surface)}</span><span>${escape(presenceLabel(session))}</span>${plan ? `<span title="${escape(planDetail(plan))}"><span aria-hidden="true">${plan.done}/${plan.total}</span><span class="sr-only">${escape(planDetail(plan))}</span></span>` : ""}${session.queueCount ? `<span>${session.queueCount} queued</span>` : ""}${holders.length ? `<span title="${escape(holders.map(holderLabel).join("; "))}">${holders.length} terminals</span>` : ""}${session.archived ? "<span>Archived</span>" : ""}</div>${heldWorkLine(session)}</button>`;
}

function heldWorkLine(session) {
  const held = session.heldWork || [];
  const work = held[0];
  if (!work) return "";
  const latest = work.log ? `${work.log.type}: ${work.log.text}` : work.text;
  const more = held.length > 1 ? ` +${held.length - 1}` : "";
  return `<div class="session-work" title="${escape(`${work.itemId} ${work.text}${work.log ? `\n${latest}` : ""}`)}"><span class="session-work-id">${escape(work.itemId)}${more}</span><span class="session-work-log">${escape(latest)}</span></div>`;
}
