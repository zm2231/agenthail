import { app } from "./state.js";

export function sessionIsCurrent(session) {
  return session.current === true && !session.archived;
}

export function scopedSessions() {
  if (app.inboxMode === "current")
    return app.state.sessions.filter(sessionIsCurrent);
  if (app.inboxMode === "projects")
    return app.state.sessions.filter((session) => session.project && !session.archived);
  return app.state.sessions.filter((session) => app.showArchived || !session.archived);
}
