import { app } from "./state.js";
import { orderedSessions, orderReady, scopeOrderKeys } from "./session-order.js";

export function scopedSessions() {
  const scoped = scopeOrderKeys();
  if (scoped.length) return scoped.flatMap(orderedSessions);
  if (app.inboxMode === "current") return orderedSessions("current");
  if (app.inboxMode === "projects") return orderedSessions("all").filter((session) => session.project);
  return app.showArchived ? [...orderedSessions("all"), ...orderedSessions("archived")] : orderedSessions("all");
}

export function defaultSession() {
  const scoped = scopeOrderKeys();
  if (scoped.length) return orderReady(scoped) ? scopedSessions()[0] || null : null;
  return scopedSessions()[0] || app.state.sessions[0] || null;
}
