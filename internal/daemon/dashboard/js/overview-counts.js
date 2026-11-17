const plural = (count, noun) => `${count} ${noun}${count === 1 ? "" : "s"}`;

export function workingNow(session) {
  return session.currentReason === "working" && !session.waitingOn;
}

export function overviewWorking(summary, sessions) {
  return summary?.workingFamilies ?? sessions.filter(workingNow).length;
}

export function overviewLine({ daemon, surfaces, sessions, working, needsYou }) {
  if (daemon?.stale) return `Showing cached data. ${daemon.refreshError || "Agent app refresh is temporarily unavailable."}`;
  if (!daemon?.running) return "Start Agenthail to deliver work.";
  const connected = surfaces.filter((surface) => surface.connected).length;
  const queued = sessions.reduce((total, session) => total + (session.queueCount || 0), 0);
  const parts = [connected ? `${plural(connected, "agent app")} connected` : "No agent apps connected"];
  if (working > 0) parts.push(`${working} working`);
  if (needsYou > 0) parts.push(`${needsYou} need${needsYou === 1 ? "s" : ""} you`);
  if (queued > 0) parts.push(`${plural(queued, "message")} queued`);
  return parts.join(" · ");
}
