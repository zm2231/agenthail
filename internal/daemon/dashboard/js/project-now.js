// Mirrors native/ProjectNow.swift; scripts/fixtures/project-now-vectors.json holds both to the same answers.
import { shortHostName } from "./hosts.js";
import { hostDown as unreachable } from "./presence.js";

const knownSince = Date.UTC(2000, 0, 1);

const running = (session) => !session.archived && (session.status === "busy" || (session.subagents?.working || 0) > 0);

function rank(session) {
  if (unreachable(session)) return 3;
  if (session.waitingOn) return 0;
  return running(session) ? 1 : 2;
}

function milliseconds(timestamp) {
  const at = Date.parse(timestamp || "");
  return at >= knownSince ? at : null;
}

function sortKey(session, rank) {
  if (rank === 0) return milliseconds(session.waitingOn?.since) ?? Number.MAX_SAFE_INTEGER;
  if (rank === 1) return -(milliseconds(session.registeredAt) ?? 0);
  return -(milliseconds(session.lastActive) ?? 0);
}

function before(left, right) {
  const leftRank = rank(left);
  const rightRank = rank(right);
  return leftRank - rightRank || sortKey(left, leftRank) - sortKey(right, rightRank) || (left.id < right.id ? -1 : left.id > right.id ? 1 : 0);
}

const key = (session) => session.checkout?.id ?? `${session.host?.id || ""}|${session.cwd || ""}`;

function place(session) {
  const checkout = session.checkout;
  if (checkout?.branch) return checkout.branch;
  if (checkout?.detachedHead) return checkout.detachedHead.slice(0, 7);
  const path = checkout?.path ?? session.cwd ?? "";
  return path ? path.split("/").filter(Boolean).pop() || "/" : "No folder";
}

function title(rows, computers, localName) {
  const first = rows[0];
  const parts = [place(first)];
  if (computers > 1) parts.push(!first.host || first.host.state === "local" ? localName : shortHostName(first.host.name));
  if (rows.every(unreachable)) parts.push("unreachable");
  else if (rows.some((session) => session.checkout?.dirty)) parts.push("modified");
  return parts.join(" · ");
}

export function projectNow(sessions, computers = 1, localName = "this computer") {
  const byKey = new Map();
  for (const session of [...sessions].sort(before)) {
    const id = key(session);
    if (!byKey.has(id)) byKey.set(id, []);
    byKey.get(id).push(session);
  }
  const groups = [...byKey].map(([id, rows]) => ({ id, title: title(rows, computers, localName), sessions: rows }));
  return { groups, count: sessions.length, showsGroups: groups.length > 1 };
}
