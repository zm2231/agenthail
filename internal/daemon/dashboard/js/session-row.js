import { holderLabel, sharedHolders } from "./holders.js";
import { livePlan, planDetail, presenceLabel, presenceTone } from "./presence.js";
import { app } from "./state.js";
import { escape, labels } from "./format.js";
import { displayName, rawDisplayName } from "./session-labels.js";
import { hostBadge } from "./hosts.js";

export function sessionButton(session) {
  const holders = sharedHolders(session);
  const plan = livePlan(session);
  return `<button class="session ${app.selected?.id === session.id ? "selected" : ""}${session.archived ? " archived" : ""}" title="${escape(rawDisplayName(session))}" type="button" data-session="${escape(session.id)}"><div class="session-name"><i class="dot ${escape(presenceTone(session))}"></i><span>${escape(displayName(session))}</span></div><div class="session-detail">${hostBadge(session)}<span>${escape(labels[session.surface] || session.surface)}</span><span>${escape(presenceLabel(session))}</span>${plan ? `<span title="${escape(planDetail(plan))}"><span aria-hidden="true">${plan.done}/${plan.total}</span><span class="sr-only">${escape(planDetail(plan))}</span></span>` : ""}${session.queueCount ? `<span>${session.queueCount} queued</span>` : ""}${holders.length ? `<span title="${escape(holders.map(holderLabel).join("; "))}">${holders.length} terminals</span>` : ""}${session.archived ? "<span>Archived</span>" : ""}</div></button>`;
}
