import { renderOverview } from "./overview.js";
import { $ } from "./dom.js";
import { escape } from "./format.js";
import { renderSessions, renderSurfaceFilter } from "./sessions.js";
import { renderStartSurfaceOptions } from "./start-form.js";
import { renderOperations } from "./operations.js";
import { app } from "./state.js";
import { renderSettings } from "./settings.js";
import { loadTools } from "./tool-loader.js";
import { responseError } from "./http.js";
import { applyRekeys } from "./rekeys.js";
import { stopLiveStream } from "./session-stream.js";
import { selectSession } from "./session-selection.js";
import { loadAudit } from "./audit.js";
import { renderChat } from "./chat.js";
import { mobileConversationView } from "./navigation.js";
import { scopedSessions } from "./session-scope.js";
import { friendlyError, toast } from "./feedback.js";

export function renderAll() {
  try {
    renderOverview();
  } catch (error) {
    console.error("dashboard overview render failed", error);
    $("#surface-cards").innerHTML =
      `<div class="empty-card">Could not render connected surfaces: ${escape(error.message || error)}</div>`;
  }
  renderSurfaceFilter();
  renderStartSurfaceOptions();
  renderSessions();
  renderOperations();
  if (app.settings && app.operationsTab === "settings") renderSettings();
  $("#sync").textContent =
    `Synced ${new Date(app.state.updatedAt || Date.now()).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}`;
}

export async function load(fresh = false, selectDefault = true) {
  if (app.loading) return;
  app.loading = true;
  const slowTimer = setTimeout(() => {
    if (app.loading && !app.hasState) {
      $("#sync").textContent = "Still connecting";
      $("#daemon-detail").textContent =
        "Surface discovery is taking a little longer";
      $("#surface-cards").innerHTML =
        '<div class="empty-state compact">Connecting to Claude, Codex, and Notion.</div>';
    }
  }, 2000);
  try {
    const [, response] = await Promise.all([
      loadTools(),
      fetch(`/api/state${fresh ? "?fresh=1" : ""}`),
    ]);
    if (!response.ok) throw await responseError(response);
    const state = await response.json();
    app.state = {
      ...state,
      surfaces: state.surfaces || [],
      sessions: state.sessions || [],
      queue: state.queue || [],
      channels: state.channels || [],
      relays: state.relays || [],
      history: state.history || [],
      attention: state.attention || [],
      deliveryProblems: state.deliveryProblems || [],
    };
    $("#daemon-presence").className = app.state.daemon?.running
      ? "daemon-presence online"
      : "daemon-presence offline";
    app.hasState = true;
    const rekeyed = state.rekeyed || {};
    applyRekeys(rekeyed);
    let followSelection = null;
    if (app.selected) {
      const replacement = app.state.sessions.find(
        (session) => session.id === app.selected.id,
      );
      if (replacement) app.selected = replacement;
      else if (rekeyed[app.selected.id]) {
        followSelection = rekeyed[app.selected.id];
        app.drafts.set(followSelection, $("#message").value);
        stopLiveStream(true);
        app.selected = null;
      }
    }
    renderAll();
    if (followSelection) await selectSession(followSelection);
    if (app.audit.loaded) await loadAudit(true, true);
    if (app.selected && app.history) renderChat();
    if (selectDefault && !app.selected && location.hash === "#conversations" && !mobileConversationView()) {
      const first = scopedSessions()[0] || app.state.sessions[0];
      if (first) selectSession(first.id);
    }
  } catch (error) {
    const message = friendlyError(error);
    $("#daemon-status").textContent = "Not connected";
    $("#daemon-presence").className = "daemon-presence offline";
    $("#daemon-detail").textContent =
      "Restart Agenthail from the menu bar, then refresh";
    $("#surface-cards").innerHTML =
      `<div class="empty-state compact">${escape(message)}</div>`;
    $("#recent-activity").innerHTML = "";
    toast(message);
  } finally {
    clearTimeout(slowTimer);
    app.loading = false;
  }
}
