import { app } from "./state.js";
import { orderedSessions, scopeOrderKeys } from "./session-order.js";

export function scopedSessions() {
  const scoped = scopeOrderKeys();
  if (scoped.length) return scoped.flatMap(orderedSessions);
  if (app.inboxMode === "current") return orderedSessions("current");
  if (app.inboxMode === "projects") return orderedSessions("all").filter((session) => session.project);
  return app.showArchived ? [...orderedSessions("all"), ...orderedSessions("archived")] : orderedSessions("all");
}
