import { retiredPresentation, rememberPresentation } from "./presentation-order.js";
import { requireDisplay } from "./session-display.js";
import { requirePosition, olderPosition } from "./session-view.js";
import { responseError } from "./http.js";
import { app } from "./state.js";

export async function readSessionDetail(id) {
  const response = await fetch(`/api/session?id=${encodeURIComponent(id)}&limit=20&timeline=1`);
  if (!response.ok) throw await responseError(response);
  const detail = await response.json();
  requireDisplay(detail.session);
  requirePosition(detail.presentationPosition);
  detail.transcriptWarning = detail.readError || "";
  return detail;
}

export function acceptSelectedPresentation(detail) {
  if (retiredPresentation(detail.presentationPosition)) return false;
  if (app.selected?.id === detail.session.id && olderPosition(detail.presentationPosition, app.selectedPosition)) return false;
  if (olderPosition(detail.presentationPosition, app.sessionView?.presentationPosition)) {
    const newer = app.sessionView.sessions.find((row) => row.id === detail.session.id);
    if (newer) { detail.session = newer; detail.presentationPosition = app.sessionView.presentationPosition; }
  }
  rememberPresentation(detail.presentationPosition);
  app.selected = detail.session;
  app.selectedPosition = detail.presentationPosition;
  return true;
}
