import { $ } from "./dom.js";
import { app } from "./state.js";
import { renderSessions, sessionBatchSize } from "./sessions.js";
import { cancelCodexSearch, startCodexSearch } from "./session-search.js";
import { syncConversationLayout } from "./navigation.js";

export function bindSessionEvents() {
  $("#session-search").addEventListener("input", () => {
    app.sessionLimit = sessionBatchSize();
    const query = $("#session-search").value.trim();
    if (query.length >= 3 && ["all", "codex"].includes(app.filters.surface)) {
      startCodexSearch(query);
    } else {
      cancelCodexSearch();
      app.codexSearch = { query: "", results: [], loading: false, error: "", timer: null, controller: null };
    }
    renderSessions();
  });
  $("#session-surface-filter").addEventListener("change", (event) => {
    app.sessionLimit = sessionBatchSize();
    app.filters.surface = event.target.value;
    const query = $("#session-search").value.trim();
    cancelCodexSearch();
    app.codexSearch = { query: "", results: [], loading: false, error: "", timer: null, controller: null };
    if (query.length >= 3 && ["all", "codex"].includes(app.filters.surface)) startCodexSearch(query);
    renderSessions();
  });
  $("#session-status-filter").addEventListener("change", (event) => {
    app.sessionLimit = sessionBatchSize();
    app.filters.status = event.target.value;
    renderSessions();
  });
  window.addEventListener("beforeunload", cancelCodexSearch);
  $("#show-archived").addEventListener("change", (event) => {
    app.showArchived = event.target.checked;
    app.sessionLimit = sessionBatchSize();
    renderSessions();
  });
  $("#session-more").addEventListener("click", () => {
    app.sessionLimit += sessionBatchSize();
    renderSessions();
  });
  $("#mobile-chat-back").addEventListener("click", () => {
    app.mobileChatOpen = false;
    syncConversationLayout();
    window.scrollTo(0, 0);
    document.querySelector(`[data-session="${CSS.escape(app.selected?.id || "")}"]`)?.focus();
  });
}
