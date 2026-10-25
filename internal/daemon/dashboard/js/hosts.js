import { escape } from "./format.js";

export function remoteHostName(session) {
  return session?.host && session.host.state !== "local" ? session.host.name : "";
}

export function shortHostName(name) {
  return String(name || "").split(".")[0];
}

export function hostBadge(session) {
  const host = remoteHostName(session);
  if (!host) return "";
  const unavailable = Boolean(session.unavailableReason);
  const short = shortHostName(host);
  return `<span class="host-badge${unavailable ? " unavailable" : ""}" title="${escape(session.unavailableReason || `On ${host}`)}">${escape(unavailable ? `${short} unavailable` : short)}</span>`;
}
