import { retiredPresentation } from "./presentation-order.js";
import { app } from "./state.js";
import { $ } from "./dom.js";
import { readSessionView, olderPosition } from "./session-view.js";
import { responseError } from "./http.js";
import { readSessionDetail } from "./session-detail.js";

export function listParameters() {
  const params = new URLSearchParams({ scope: app.showArchived ? "archived" : app.inboxMode === "projects" ? "all" : app.inboxMode });
  const query = $("#session-search").value.trim();
  if (query) params.set("q", query);
  if (app.inboxMode !== "projects") {
    for (const key of ["surface", "status"]) if (app.filters[key] !== "all") params.set(key, app.filters[key]);
  }
  if (app.selected) params.set("selectedId", app.selected.id);
  return params;
}

export async function readDashboardViews(params, selectedID) {
  const metadata = async () => {
    const response = await fetch("/api/state?metadata=1");
    if (!response.ok) throw await responseError(response);
    const state = await response.json();
    for (const field of ["surfaces", "queue", "channels", "relays", "history", "attention", "deliveryProblems", "projects"]) if (!Array.isArray(state[field])) throw Error(`Missing dashboard metadata: ${field}`);
    if (!state.daemon) throw Error("Missing daemon metadata");
    return state;
  };
  const detail = selectedID ? await readSessionDetail(selectedID).catch((error) => ({ error })) : null;
  const [state, sessionView, overviewView] = await Promise.all([
    metadata(), readSessionView(params), readSessionView({ scope: "current" }),
  ]);
  if (retiredPresentation(sessionView.presentationPosition) || retiredPresentation(overviewView.presentationPosition) || olderPosition(sessionView.presentationPosition, app.sessionView?.presentationPosition)) throw Object.assign(Error("Stale session presentation"), { retry: true });
  if (app.seenCatalog?.epoch === sessionView.hostEpoch && sessionView.catalogSeq < app.seenCatalog.sequence) throw Object.assign(Error("Stale catalog view"), { retry: true });
  if (olderPosition(sessionView.presentationPosition, app.selectedPosition)) throw Object.assign(Error("Stale list presentation"), { retry: true });
  const selected = sessionView.sessions.find((row) => row.id === selectedID);
  if (selected && detail) {
    detail.session = selected;
    detail.presentationPosition = sessionView.presentationPosition;
  }
  return { state, sessionView, overviewView, detail };
}
