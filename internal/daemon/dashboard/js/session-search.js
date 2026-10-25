import { app } from "./state.js";
import { responseError } from "./http.js";
import { friendlyError } from "./feedback.js";
import { renderSessions } from "./sessions.js";

export function cancelCodexSearch() {
  clearTimeout(app.codexSearch.timer);
  app.codexSearch.controller?.abort();
}

export function startCodexSearch(query) {
  cancelCodexSearch();
  const search = { query: query.toLowerCase(), results: [], loading: true, error: "", timer: null, controller: null };
  app.codexSearch = search;
  search.timer = setTimeout(async () => {
    const controller = new AbortController();
    search.controller = controller;
    try {
      const response = await fetch(`/api/search?surface=codex&q=${encodeURIComponent(query)}`, { signal: controller.signal });
      if (!response.ok) throw await responseError(response);
      const payload = await response.json();
      if (app.codexSearch !== search) return;
      search.results = (payload.results || []).map((result) => ({ ...result.session, snippet: result.snippet }));
      search.error = payload.remoteError || "";
    } catch (error) {
      if (error.name !== "AbortError" && app.codexSearch === search) search.error = friendlyError(error);
    } finally {
      if (app.codexSearch === search) {
        search.loading = false;
        renderSessions();
      }
    }
  }, 300);
}
