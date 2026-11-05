import { app } from "./state.js";
import { orderedSessions } from "./session-order.js";

export function scopedSessions() {
  if (app.inboxMode === "current") return orderedSessions("current");
  if (app.inboxMode === "projects") return orderedSessions("all").filter((session) => session.project);
  return app.showArchived ? [...orderedSessions("all"), ...orderedSessions("archived")] : orderedSessions("all");
}
