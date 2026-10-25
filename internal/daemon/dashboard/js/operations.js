import { statusLabel, statusPill } from "./presence.js";
import { app } from "./state.js";
import { $ } from "./dom.js";
import { escape, timeAgo } from "./format.js";
import { auditKindLabel, loadAudit, renderAudit } from "./audit.js";
import { loadSettings, renderSettings } from "./settings.js";
import { action } from "./actions.js";
import { friendlyError, toast } from "./feedback.js";
import { load } from "./dashboard-state.js";

export function queueReason(item) {
	if (item.evidence === "transport_accepted") return "Transport accepted; receiver completion is pending";
	if (item.evidence === "unknown") return item.historical ? "Delivery was never confirmed" : "Submitted";
	if (item.evidence === "failed") return "Delivery failed";
	if (item.status !== "pending") return statusLabel(item.status);
  if (item.heldBy === "hostUnreachable")
    return "Held: this agent's host is unreachable";
  const target = app.state.sessions.find(
    (session) => session.id === item.sessionId,
  );
  if (target?.readOnly) return target.readOnlyReason || "Target is not writable";
  if (target?.surface === "codex" && target?.status === "notLoaded") {
    return target.transport === "desktop"
      ? "Loading this Codex conversation before delivery"
      : "Codex conversation is not available to load";
  }
  return (
    {
      waiting: "Held: this agent is waiting on you",
      busy: "Held: this agent is working",
      offline: "Held: this agent is offline",
      unknown: "Held: this agent's state is unknown",
      refused: "Held: this agent refused to start",
      retrying: "Held: waiting to retry",
      behind: "Held: behind an earlier message",
      notLoaded: "Held: this conversation is not loaded",
      hostUnreachable: "Held: this agent's host is unreachable",
    }[item.heldBy] || "Delivers on the next scan"
  );
}

export function renderOperations() {
  const { queue, channels, relays, history = [] } = app.state;
  $("#operations-queue-count").textContent = queue.length;
  $("#operations-relay-count").textContent = relays.length;
  $("#operations-history-count").textContent = history.length === 50 ? "50+" : history.length;
  $("#queue-list").innerHTML =
    queue
      .map(
        (item) =>
          `<article class="operation-item"><div class="operation-main"><div class="operation-title"><i class="operation-dot ${escape(item.status)}"></i>${escape(item.target)}</div><div class="operation-detail queue-message">${escape(item.message)}${item.lastError ? `<br><span class="operation-error">${escape(item.lastError)}</span>` : ""}</div><div class="operation-meta">${escape(queueReason(item))} · queued ${timeAgo(item.queuedAt)}</div></div><div class="operation-actions">${statusPill(item.status)}${item.status === "dead" ? `<button class="button quiet" data-retry="${item.id}" type="button">Retry</button>` : ""}${item.status === "pending" ? `<button class="button quiet" data-cancel="${item.id}" type="button">Cancel</button>` : ""}</div></article>`,
      )
      .join("") ||
    '<div class="empty-card">Nothing is waiting to be delivered.</div>';
  const outcomes = history
    .filter((entry) => entry.evidence)
    .slice(0, 8);
  $("#delivery-history").innerHTML =
    outcomes
      .map((entry) => {
        const label = {
          queued: "Queued",
          transport_accepted: "Transport accepted",
          held: "Held by the receiver",
          delivered: "Delivered",
          reply_observed: "Reply observed",
          failed: "Failed",
          unknown: "Never confirmed",
          canceled: "Canceled",
        }[entry.evidence] || auditKindLabel(entry.kind);
        const detail = entry.error || entry.message || entry.result || "No details recorded";
        return `<article class="delivery-event"><div class="delivery-event-heading"><strong>${escape(label)} · ${escape(entry.target || "Agenthail")}</strong><time>${timeAgo(entry.createdAt)}</time></div><p>${escape(detail)}</p></article>`;
      })
      .join("") ||
    '<div class="empty-card">No delivery outcomes have been recorded yet.</div>';
  $("#channel-list").innerHTML =
    channels
      .map(
        (channel) =>
          `<article class="operation-item"><div class="operation-main"><div class="operation-title">#${escape(channel.name)}</div><div class="operation-detail">${escape(channel.members.join(" · ") || "No members yet")}</div></div><button class="button quiet" data-network-action="channel-delete" data-channel="${escape(channel.name)}" type="button">Remove</button></article>`,
      )
      .join("") || '<div class="empty-card">No shared channels yet.</div>';
  $("#channel-actions").hidden = channels.length === 0;
  $("#relay-list").innerHTML =
    relays
      .map(
        (relay) =>
          `<article class="operation-item"><div class="operation-main"><div class="operation-title">${escape(relay.from)} <span class="route-arrow">→</span> ${escape(relay.to)}</div><div class="operation-detail">${relay.handoff === "goal_completed" ? "Goal completed" : "Turn finished"} · Matches /${escape(relay.pattern)}/ · ${relay.once ? "once" : "persistent"} · ${relay.active ? "active" : "complete"} · queued ${relay.fireCount || 0} · pending ${relay.pendingCount || 0}${relay.lastError ? ` · ${escape(relay.lastError)}` : ""}${relay.lastFiredAt ? ` · last ${escape(relay.lastFiredAt)}` : ""}</div></div><button class="button quiet" data-network-action="relay-remove" data-relay-id="${relay.id}" type="button">Remove</button></article>`,
      )
      .join("") || '<div class="empty-card">No automatic handoffs yet.</div>';
  renderAudit();
  renderSettings();
  document
    .querySelectorAll("[data-operations-panel]")
    .forEach((panel) =>
      panel.classList.toggle(
        "hidden",
        panel.dataset.operationsPanel !== app.operationsTab,
      ),
    );
  document
    .querySelectorAll("[data-operations-tab]")
    .forEach((tab) =>
      tab.classList.toggle(
        "active",
        tab.dataset.operationsTab === app.operationsTab,
      ),
    );
  document
    .querySelectorAll("[data-operations-tab]")
    .forEach((tab) =>
      tab.setAttribute(
        "aria-pressed",
        String(tab.dataset.operationsTab === app.operationsTab),
      ),
    );
}

export function bindOperationEvents() {
  document.addEventListener("submit", async (event) => {
    const form = event.target.closest("[data-network-form]");
    if (!form) return;
    event.preventDefault();
    const actionName = form.dataset.networkForm;
    const values = Object.fromEntries(new FormData(form).entries());
    if (actionName === "relay-add") values.once = values.once === "true";
    try {
      await action(actionName, values);
      toast(actionName === "relay-add" ? "Relay added." : "Network saved.");
      form.reset();
      await load();
    } catch (error) {
      toast(error.message);
    }
  });
  document.addEventListener("click", async (event) => {
    const button = event.target.closest("[data-network-action]");
    if (!button) return;
    button.disabled = true;
    try {
      await action(button.dataset.networkAction, {
        channel: button.dataset.channel,
        relayId: Number(button.dataset.relayId || 0),
      });
      toast("Network updated.");
      await load();
    } catch (error) {
      toast(friendlyError(error));
    }
  });
  document.addEventListener("click", (event) => {
    const tab = event.target.closest("[data-operations-tab]");
    if (tab) {
      app.operationsTab = tab.dataset.operationsTab;
      renderOperations();
      if (app.operationsTab === "audit" && !app.audit.loaded) loadAudit();
      if (app.operationsTab === "settings") loadSettings();
    }
  });
}
