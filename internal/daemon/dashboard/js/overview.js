import { app } from "./state.js";
import { $ } from "./dom.js";
import { escape, labels, surfaceIcon, timeAgo } from "./format.js";
import { orderedSessions } from "./session-order.js";
import { backgroundWaiting, presenceLabel, presenceTone } from "./presence.js";
import { displayName } from "./session-labels.js";
import { refreshDecisions, renderInbox } from "./inbox.js";

export { renderDeliveryProblems, renderLeadReplacements } from "./inbox.js";

export function renderOverview() {
  const { surfaces, sessions } = app.state;
  const connected = surfaces.filter((surface) => surface.connected).length;
  const working = sessions.filter(
    (session) => session.currentReason === "working",
  ).length;
  const queued = sessions.reduce(
    (total, session) => total + session.queueCount,
    0,
  );
  const surfaceStatus =
    connected === 0
      ? "No surfaces connected"
      : `${connected} surface${connected === 1 ? "" : "s"} connected`;
  const activityStatus = [
    working > 0
      ? `${working} agent${working === 1 ? "" : "s"} working`
      : "",
    queued > 0
      ? `${queued} message${queued === 1 ? "" : "s"} waiting`
      : "",
  ].filter(Boolean);
  $("#daemon-status").textContent = app.state.daemon?.running
    ? "Connected"
    : "Not running";
  $("#daemon-detail").textContent = app.state.daemon?.stale
    ? `Showing cached data. ${app.state.daemon.refreshError || "Surface refresh is temporarily unavailable"}`
    : app.state.daemon?.running
      ? [surfaceStatus, ...activityStatus].join(" · ") || "Nothing needs delivery"
      : "Start Agenthail to deliver work";
  $("#surface-cards").innerHTML =
    surfaces
      .map((surface) => {
        const owned = sessions.filter(
          (session) => session.surface === surface.name,
        );
        const active = owned.filter(
          (session) => session.currentReason === "working",
        ).length;
        const present = owned.filter((session) =>
          surface.name === "claude"
            ? session.open
            : session.currentReason === "recent",
        ).length;
        const queued = owned.reduce(
          (total, session) => total + session.queueCount,
          0,
        );
        const name = labels[surface.name] || surface.name;
        const presenceWindow =
          surface.name === "claude"
            ? "Open now"
            : `Past ${app.state.codexRecentHours || 5}h`;
        const healthLabel = surface.health === "degraded"
          ? "Needs attention"
          : surface.health === "signin_off"
            ? "Sign-in off"
            : surface.health === "signin_unavailable"
            ? "No browser here"
            : surface.connected
            ? "Connected"
            : "Not connected";
        return `<button class="surface-row" type="button" data-surface="${escape(surface.name)}"><div class="surface-identity"><span class="surface-logo ${escape(surface.name)}">${surfaceIcon(surface.name)}</span><div><div class="surface-name">${escape(name)}</div><div class="connection-line ${escape(surface.health || (surface.connected ? "healthy" : "unavailable"))}" title="${escape(surface.healthDetail || surface.error || "")}"><i></i>${escape(healthLabel)}</div></div></div><div class="surface-stat"><span>Working</span><strong>${active}</strong></div><div class="surface-stat"><span>Current</span><strong>${present}</strong><small>${presenceWindow}</small></div><div class="surface-stat"><span>Queued</span><strong>${queued}</strong></div></button>`;
      })
      .join("") ||
    '<div class="empty-state compact">No surfaces are connected yet.</div>';
  const recent = orderedSessions("current").slice(0, 8);
  $("#recent-activity").innerHTML =
    recent
      .map(
        (session) =>
          `<button class="activity-item" type="button" data-session="${escape(session.id)}"><div class="activity-main"><div class="activity-name"><i class="dot ${escape(presenceTone(session))}"></i><span>${escape(displayName(session))}</span></div><div class="activity-detail">${escape(labels[session.surface] || session.surface)} · ${session.status === "busy" ? "working now" : timeAgo(session.lastActive)}</div></div><span class="status-pill ${escape(backgroundWaiting(session) ? "background" : session.currentReason || session.status)}">${escape(presenceLabel(session))}</span></button>`,
      )
      .join("") ||
    '<p class="empty-inline">No conversations are active right now.</p>';
  renderInbox();
  refreshDecisions();
}
