import { renderOverview } from "./overview.js";
import { $ } from "./dom.js";
import { escape } from "./format.js";
import { renderSessions, renderSurfaceFilter } from "./sessions.js";
import { renderStartSurfaceOptions } from "./start-form.js";
import { renderOperations } from "./operations.js";
import { app } from "./state.js";
import { renderSettings } from "./settings.js";
import { loadTools } from "./tool-loader.js";
import { retryPendingSends, unconfirmedSendsOutside } from "./pending-send.js";
import { applyRekeys } from "./rekeys.js";
import { stopLiveStream } from "./session-stream.js";
import { selectSession } from "./session-selection.js";
import { loadAudit } from "./audit.js";
import { renderChat } from "./chat.js";
import { mobileConversationView } from "./navigation.js";
import { defaultSession } from "./session-scope.js";
import { listedRows, loadSessionOrder, orderIsMissing } from "./session-order.js";
import { findSessions, readHistory } from "./session-pages.js";
import { catalogRead } from "./catalog-live.js";
import { rereadSoon } from "./order-refresh.js";
import { friendlyError, toast } from "./feedback.js";
import { refreshShownProject } from "./project-pane.js";
import { dropMissingScope, renderTiles } from "./project-tiles-view.js";

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
  dropMissingScope();
  renderSessions();
  renderTiles();
  refreshShownProject();
  renderOperations();
  if (app.settings && app.operationsTab === "settings") renderSettings();
  $("#sync").textContent =
    `Synced ${new Date(app.state.updatedAt || Date.now()).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}`;
}

export function ensureSessionOrder() {
  if (app.hasState && orderIsMissing()) load();
}

export async function load(fresh = false, selectDefault = true) {
  if (app.loading) {
    app.loadAgain = { fresh: fresh || Boolean(app.loadAgain?.fresh), selectDefault: selectDefault && (app.loadAgain?.selectDefault ?? true) };
    return;
  }
  app.loading = true;
  let loaded = false;
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
    const [, { base: state, refreshAt, catalogSeq }, history] = await Promise.all([
      loadTools(),
      loadSessionOrder(fresh),
      readHistory(),
    ]);
    clearTimeout(app.orderRefresh);
    if (Number.isFinite(refreshAt)) app.orderRefresh = setTimeout(() => rereadSoon(0), Math.max(0, refreshAt - Date.now()));
    const rows = listedRows();
    const kept = [app.selected?.id, ...unconfirmedSendsOutside((id) => rows.has(id)).map((item) => item.sessionId), ...(state.deliveryProblems || []).map((item) => item.sessionId)];
    const missing = [...new Set(kept)].filter((id) => id && !rows.has(id) && !state.rekeyed?.[id]);
    if (missing.length) for (const [id, row] of await findSessions(missing).catch(() => new Map())) rows.set(id, row);
    app.state = {
      ...state,
      counts: app.sessionOrder.current.counts,
      history,
      surfaces: state.surfaces || [],
      sessions: [...rows.values()],
      queue: state.queue || [],
      channels: state.channels || [],
      relays: state.relays || [],
      attention: state.attention || [],
      deliveryProblems: state.deliveryProblems || [],
      leadReplacements: state.leadReplacements || [],
    };
    catalogRead(catalogSeq);
    $("#daemon-presence").className = app.state.daemon?.running
      ? "daemon-presence online"
      : "daemon-presence offline";
    app.hasState = true;
    const rekeyed = state.rekeyed || {};
    applyRekeys(rekeyed);
    retryPendingSends();
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
    loaded = true;
    if (followSelection) await selectSession(followSelection);
    if (app.audit.loaded) await loadAudit(true, true);
    if (app.selected && app.history) renderChat();
    if (selectDefault && !app.selected && location.hash === "#conversations" && !mobileConversationView()) {
      const first = defaultSession();
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
    const again = app.loadAgain;
    app.loadAgain = null;
    if (again) load(again.fresh, again.selectDefault);
    else if (loaded) ensureSessionOrder();
  }
}
