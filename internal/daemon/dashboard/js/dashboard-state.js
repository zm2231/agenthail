import { rememberPresentation } from "./presentation-order.js";
import { renderOverview } from "./overview.js";
import { $ } from "./dom.js";
import { escape } from "./format.js";
import { renderSessions, renderSurfaceFilter } from "./sessions.js";
import { renderStartSurfaceOptions } from "./start-form.js";
import { renderOperations } from "./operations.js";
import { app } from "./state.js";
import { renderSettings } from "./settings.js";
import { loadTools } from "./tool-loader.js";
import { applyRekeys } from "./rekeys.js";
import { selectSession } from "./session-selection.js";
import { loadAudit } from "./audit.js";
import { renderChat } from "./chat.js";
import { mobileConversationView } from "./navigation.js";
import { viewSessions } from "./session-view.js";
import { listParameters, readDashboardViews } from "./dashboard-view-loader.js";
import { acceptSelectedPresentation } from "./session-detail.js";
import { followCatalog } from "./catalog-updates.js";
import { friendlyError } from "./feedback.js";

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

let generation = 0;
let pending = null;
let dirty = false;
let refreshTimer;

export function load() {
  generation++;
  dirty = true;
  if (!pending) pending = refreshLoop().finally(() => { pending = null; });
  return pending;
}

async function refreshLoop() {
  while (dirty) {
    dirty = false;
    const request = generation;
    const params = listParameters();
    const selectedID = app.selected?.id;
    try {
      await loadTools();
      const loaded = await readDashboardViews(params, selectedID);
      if (request !== generation || params.toString() !== listParameters().toString()) { dirty = true; continue; }
      rememberPresentation(loaded.sessionView.presentationPosition);
      app.state = { ...loaded.state, sessions: loaded.sessionView.sessions };
      app.sessionView = loaded.sessionView;
      app.overviewView = loaded.overviewView;
      app.hasState = true;
      applyRekeys(loaded.state.rekeyed || {});
      if (loaded.state.rekeyed?.[selectedID]) {
        app.drafts.set(loaded.state.rekeyed[selectedID], $("#message").value);
        selectSession(loaded.state.rekeyed[selectedID]);
      } else if (loaded.detail?.session && app.selected?.id === selectedID) acceptSelectedPresentation(loaded.detail);
      if (loaded.detail?.error && app.history && app.selected?.id === selectedID) app.history.transcriptWarning = "Could not refresh the conversation transcript.";
      $("#daemon-presence").className = "daemon-presence online";
      renderAll();
      if (app.selected && app.history) renderChat();
      followCatalog(loaded.sessionView);
      clearTimeout(refreshTimer);
      const deadlines = [loaded.sessionView.view.refreshAt, loaded.overviewView.view.refreshAt].filter(Boolean).map(Date.parse);
      if (deadlines.length) refreshTimer = setTimeout(load, Math.max(50, Math.min(2147483647, Math.min(...deadlines) - Date.now())));
      if (app.audit.loaded) await loadAudit(true, true);
      if (!app.selected && location.hash === "#conversations" && !mobileConversationView()) {
        const first = viewSessions(app.sessionView)[0];
        if (first) selectSession(first.id);
      }
    } catch (error) {
      if (request !== generation) { dirty = true; continue; }
      $("#daemon-status").textContent = "Not connected";
      $("#daemon-presence").className = "daemon-presence offline";
      $("#daemon-detail").textContent = app.hasState ? "Showing the last complete view. Reconnecting…" : friendlyError(error);
      $("#sync").textContent = "Not synced";
      console.error("dashboard refresh failed", error);
      if (error.retry || error.status === 409) { clearTimeout(refreshTimer); refreshTimer = setTimeout(load, 250); }
    }
  }
}
