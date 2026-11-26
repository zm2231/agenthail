import { loadSignin } from "./signin.js";
import { loadDevices } from "./devices.js";
import { loadBrowserPush } from "./browser-push.js";
import { loadClaudeCompact } from "./claude-settings.js";
import { responseError } from "./http.js";
import { app } from "./state.js";
import { $ } from "./dom.js";
import { escape } from "./format.js";
import { renderSurfaceHealth } from "./surface-health.js";

export async function loadSettings() {
  loadSignin();
  loadDevices();
  loadBrowserPush();
  loadClaudeCompact();
  try {
    const response = await fetch("/api/settings");
    if (!response.ok) throw await responseError(response);
    app.settings = await response.json();
    renderSettings();
  } catch (error) {
    $("#remote-access-content").innerHTML = `<div class="empty-card">${escape(error.message)}</div>`;
  }
}

export function dashboardConfigChange(saved, codexRecentHours, busyDelivery) {
  const change = {};
  if (codexRecentHours !== saved.codexRecentHours) change.codexRecentHours = codexRecentHours;
  if (busyDelivery !== (saved.busyDelivery || "queue")) change.busyDelivery = busyDelivery;
  return Object.keys(change).length ? { action: "dashboard-config", ...change } : null;
}

export function renderClaudeDefaultRoot(chosen, roots) {
  const select = $("#settings-claude-default-root");
  select.innerHTML = [`<option value="">The primary root</option>`, ...roots.map(name => `<option value="${escape(name)}">${escape(name)}</option>`)].join("");
  select.value = chosen || "";
}

const agentModes = [["auto", "Automatic (when installed)"], ["on", "On"], ["off", "Off"]];
const agentStates = { on: ["healthy", "In use"], off: ["agent_off", "Off"], unavailable: ["agent_unavailable", "Not installed here"], missing: ["unavailable", "Missing"] };

export function renderAgents(agents) {
  $("#agents-list").innerHTML = agents.map((agent) => {
    const options = agentModes.map(([mode, label]) => `<option value="${mode}"${agent.mode === mode ? " selected" : ""}>${label}</option>`).join("");
    const [stateClass, stateLabel] = agentStates[agent.state] || agentStates.missing;
    const detail = agent.state === "missing" ? `<p class="signin-problem">${escape(agent.detail || "")}</p>` : agent.state === "unavailable" ? `<p class="signin-resolved">${escape(agent.detail || "")}</p>` : "";
    return `<div class="signin-consumer"><div><strong>${escape(agent.label)}</strong><span class="health-state ${stateClass}">${stateLabel}</span>${detail}</div><label><span class="sr-only">Setting for ${escape(agent.label)}</span><select data-agent-mode="${escape(agent.id)}">${options}</select></label></div>`;
  }).join("") || '<div class="empty-card">No agents run on this computer.</div>';
}

export function renderSettings() {
  if (!app.settings) return;
  const { dashboard, remoteAccess } = app.settings;
  $("#settings-codex-hours").value = String(dashboard.codexRecentHours);
  $("#settings-busy-delivery").value = String(dashboard.busyDelivery || "queue");
  $("#settings-resume-after-limit").checked = dashboard.resumeAfterLimit !== false;
  $("#settings-move-after-limit").checked = dashboard.moveAfterLimit !== false;
  renderClaudeDefaultRoot(app.settings.claudeDefaultRoot, app.settings.claudeRoots || []);
  renderAgents(app.settings.agents || []);
  $("#settings-listener").textContent = dashboard.listen;
  renderSurfaceHealth();
  const remoteQR = app.remoteQRVisible
    ? `<div class="remote-qr"><img src="/api/settings/remote-qr" alt="QR code for this Agenthail dashboard"><button class="soft-button remote-qr-button" data-hide-remote-qr type="button">Hide QR</button></div>`
    : `<button class="remote-qr-cover" data-reveal-remote-qr type="button"><span>QR hidden</span><strong>Reveal phone QR</strong></button>`;
  const remoteName = app.remoteQRVisible
    ? `<p>${escape(remoteAccess.dnsName)}</p>`
    : `<p>Phone link hidden</p>`;
  $("#remote-access-content").innerHTML = remoteAccess.enabled
    ? `<div class="remote-ready">${remoteQR}<div><span class="status-pill idle">Connected</span>${remoteName}<div class="remote-actions"><button class="primary-button" data-copy-remote type="button">Copy phone link</button><button class="soft-button" data-remote-action="remote-disable" type="button">Turn off</button></div><small>On iPhone, open the link, tap Share, then Add to Home Screen.</small></div></div>`
    : `<div class="remote-off"><p>${escape(remoteAccess.error || "Use Tailscale to reach this dashboard from your own devices.")}</p><button class="primary-button" data-remote-action="remote-enable" type="button">Enable phone access</button></div>`;
}

export function hideRemoteQR() {
  if (app.remoteQRHideTimer) clearTimeout(app.remoteQRHideTimer);
  app.remoteQRHideTimer = null;
  if (!app.remoteQRVisible) return;
  app.remoteQRVisible = false;
  renderSettings();
}

export function revealRemoteQR() {
  if (app.remoteQRHideTimer) clearTimeout(app.remoteQRHideTimer);
  app.remoteQRVisible = true;
  app.remoteQRHideTimer = setTimeout(hideRemoteQR, 60000);
  renderSettings();
}

export async function createLoginLink() {
  const response = await fetch("/api/v1/dashboard/login-links", { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ remote: true }) });
  if (!response.ok) throw Error((await response.json().catch(() => ({}))).error?.message || "Could not create a phone link.");
  return (await response.json()).url;
}

export async function updateSettings(body) {
  const response = await fetch("/api/settings", { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(body) });
  if (!response.ok) throw await responseError(response);
  await loadSettings();
}
