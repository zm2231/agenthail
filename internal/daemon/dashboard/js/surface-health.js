import { app } from "./state.js";
import { $ } from "./dom.js";
import { escape, labels, surfaceIcon } from "./format.js";
import { signinOffHint } from "./signin.js";

export function renderSurfaceHealth() {
  const surfaces = app.state.surfaces || [];
  $("#surface-health-list").innerHTML = surfaces.map((surface) => {
    const name = labels[surface.name] || surface.name;
    const health = surface.health || (surface.connected ? "healthy" : "unavailable");
    const state = health === "healthy" ? "Healthy" : health === "degraded" ? "Needs attention" : health === "signin_off" ? "Sign-in off" : health === "signin_unavailable" ? "No browser here" : "Unavailable";
    const rawDetail = surface.healthDetail || surface.error || "Ready";
    const detail = health === "signin_off"
      ? signinOffHint(rawDetail)
      : health === "signin_unavailable"
      ? rawDetail.replace(/^[^:]*: /, "")
      : /cookie bridge/i.test(rawDetail)
      ? "Claude Code needs to reconnect to your signed-in account."
      : /notion.*(?:401|context)/i.test(rawDetail)
        ? "Notion is not signed in on this computer."
        : /reachable but not supervised/i.test(rawDetail)
          ? "The Codex background service will stop after a restart."
          : rawDetail;
    const runtime = surface.runtime?.name
      ? `<p><strong>${escape(surface.runtime.name)}</strong> ${escape(surface.runtime.reachable ? surface.runtime.durable ? "stays available in the background" : "is open now but will not restart automatically" : "is not connected")}</p>${surface.runtime.remediation ? `<p>How to fix: ${escape(surface.runtime.remediation)}</p>` : ""}${(surface.runtime.notes || []).map((note) => `<p>${note.message === rawDetail ? "" : `${escape(note.message)}<br>`}${note.remediation ? `How to fix: ${escape(note.remediation)}` : ""}</p>`).join("")}`
      : "";
    const repair = surface.repairAction
      ? `<button class="soft-button" data-surface-repair="${escape(surface.repairAction)}" type="button">${escape(surface.repairLabel || "Repair")}</button>`
      : "";
    return `<article class="surface-health-row"><div class="surface-health-icon ${escape(health)}">${surfaceIcon(surface.name)}</div><div class="surface-health-copy"><div><strong>${escape(name)}</strong><span class="health-state ${escape(health)}">${escape(state)}</span></div><p>${escape(detail)}</p>${runtime}</div>${repair}</article>`;
  }).join("") || '<div class="empty-card">No surfaces are configured.</div>';
}
