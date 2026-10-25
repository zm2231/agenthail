import { $ } from "./dom.js";
import { app } from "./state.js";
import { load } from "./dashboard-state.js";
import { cancelCodexSearch, startCodexSearch } from "./session-search.js";
import { syncConversationLayout } from "./navigation.js";

export function bindSessionEvents() {
  $("#session-search").addEventListener("input", () => {
    const query = $("#session-search").value.trim();
    if (query.length >= 3 && ["all", "codex"].includes(app.filters.surface)) {
      startCodexSearch(query);
    } else {
      cancelCodexSearch();
      app.codexSearch = { query: "", results: [], loading: false, error: "", timer: null, controller: null };
    }
    load();
  });
  $("#session-surface-filter").addEventListener("change", (event) => {
    app.filters.surface = event.target.value;
    const query = $("#session-search").value.trim();
    cancelCodexSearch();
    app.codexSearch = { query: "", results: [], loading: false, error: "", timer: null, controller: null };
    if (query.length >= 3 && ["all", "codex"].includes(app.filters.surface)) startCodexSearch(query);
    load();
  });
  $("#session-status-filter").addEventListener("change", (event) => {
    app.filters.status = event.target.value;
    load();
  });
  window.addEventListener("beforeunload", cancelCodexSearch);
  $("#show-archived").addEventListener("change", (event) => {
    app.showArchived = event.target.checked;
    load();
  });
  $("#mobile-chat-back").addEventListener("click", () => {
    app.mobileChatOpen = false;
    syncConversationLayout();
    window.scrollTo(0, 0);
    document.querySelector(`[data-session="${CSS.escape(app.selected?.id || "")}"]`)?.focus();
  });
}
