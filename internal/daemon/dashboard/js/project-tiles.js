const separator = "::";

export function projectOfDecision(item, projects) {
  const named = item.projectId;
  if (!named) return null;
  const host = item.hostId || "";
  const prefix = host + separator;
  const id = host && named.startsWith(prefix) ? named.slice(prefix.length) : named;
  if (projects.some((project) => project.id === named)) return named;
  if (!host) return null;
  const names = new Set([named, id]);
  const found = projects.find((project) =>
    (project.checkouts || []).some((checkout) => checkout.host?.id === host && (names.has(checkout.projectId) || names.has(checkout.linkedFrom?.id)))
    || (project.linked || []).some((ref) => names.has(ref.id)));
  return found ? found.id : null;
}

export function needsYouByProject(decisions, projects) {
  const counts = {};
  for (const item of decisions) {
    const project = projectOfDecision(item, projects);
    if (project) counts[project] = (counts[project] || 0) + 1;
  }
  return counts;
}

function byName(a, b) {
  return String(a.displayName || a.id).localeCompare(String(b.displayName || b.id), undefined, { numeric: true, sensitivity: "base" });
}

export function withActivity(projects, activity) {
  const byID = new Map(activity.map((entry) => [entry.id, entry]));
  return projects.map((project) => ({ ...project, lastActive: byID.get(project.id)?.lastActive, sessionCount: byID.get(project.id)?.sessionCount ?? 0 }));
}

export const leadRecentWindow = 7 * 24 * 60 * 60 * 1000;

export function leadSessionID(lead) {
  if (!lead?.sessionId) return "";
  if (!lead.host || lead.host.state === "local" || lead.sessionId.includes(separator)) return lead.sessionId;
  return `${lead.host.id}${separator}${lead.sessionId}`;
}

export function leadState(session) {
  return {
    lastActive: Date.parse(session.lastActive || "") || 0,
    active: Boolean(session.waitingOn) || session.currentReason === "working" || session.currentReason === "queued",
  };
}

export function tileLeads(projects, sessions) {
  const byID = new Map(sessions.map((session) => [session.id, session]));
  const leads = {};
  for (const project of projects) {
    const session = byID.get(leadSessionID((project.coordinators || [])[0]));
    if (session) leads[project.id] = leadState(session);
  }
  return leads;
}

export function tileColumns(width) {
  const usable = Number.isFinite(width) ? Math.max(width, 0) : 0;
  return Math.max(3, Math.floor((usable + 2) / 52));
}

export function fillTiles(columns, order) {
  const slots = 2 * columns - 2;
  const all = order(slots);
  return all.more.length ? order(slots - 1) : all;
}

export function orderTiles(projects, limit, { waiting = {}, leads = {}, now = Date.now() } = {}) {
  const active = (project) => (project.sessionCount || 0) > 0 || (project.coordinators || []).length > 0 || (waiting[project.id] || 0) > 0;
  const group = (project) => {
    if ((waiting[project.id] || 0) > 0) return 0;
    const lead = leads[project.id];
    if (lead && (lead.active || (lead.lastActive && now - lead.lastActive <= leadRecentWindow))) return 1;
    return active(project) ? 2 : 3;
  };
  const time = (project) => Math.max(Date.parse(project.lastActive || "") || 0, leads[project.id]?.lastActive || 0);
  const open = projects.filter((project) => !project.archived);
  const recent = open.sort((a, b) => group(a) - group(b) || time(b) - time(a) || byName(a, b));
  const shown = recent.slice(0, limit);
  const more = projects.filter((project) => !shown.some((tile) => tile.id === project.id)).sort(byName);
  return { shown, more };
}

export const projectColourCount = 6;

export function projectColourID(projectID, ownerHostID) {
  const id = String(projectID ?? "");
  if (!ownerHostID || !id || id.includes(separator) || id.startsWith("repo:")) return id;
  return `${ownerHostID}${separator}${id}`;
}

export function projectColour(projectID) {
  let hash = 2166136261;
  for (const part of encodeURIComponent(String(projectID ?? "")).match(/%[0-9A-F]{2}|./g) || []) {
    const byte = part.length === 3 ? parseInt(part.slice(1), 16) : part.charCodeAt(0);
    hash = Math.imul(hash ^ byte, 16777619) >>> 0;
  }
  return hash % projectColourCount;
}

export function tileLetter(name) {
  const found = Array.from(String(name || "")).find((character) => /[\p{L}\p{N}]/u.test(character));
  return found ? found.toUpperCase() : "·";
}

export function arrangeTiles(projects, limit, order = [], pinned = []) {
  const place = new Map();
  order.forEach((id, index) => { if (!place.has(id)) place.set(id, index); });
  const pins = new Set(pinned);
  const sorted = [...projects].sort((a, b) =>
    Number(pins.has(b.id)) - Number(pins.has(a.id))
    || (place.get(a.id) ?? Infinity) - (place.get(b.id) ?? Infinity)
    || byName(a, b));
  const shown = sorted.filter((project) => !project.archived).slice(0, Math.max(0, limit));
  const held = new Set(shown.map((project) => project.id));
  return { shown, more: sorted.filter((project) => !held.has(project.id)) };
}

export function movingTile(id, target, order) {
  if (id === target || !order.includes(target)) return order;
  const next = order.filter((entry) => entry !== id);
  next.splice(next.indexOf(target), 0, id);
  return next;
}

export function resolveTileChange({ pinned = [] }, change, arranged) {
  if (change.kind !== "step") return change;
  const pins = new Set(pinned);
  const group = arranged.filter((id) => pins.has(id) === pins.has(change.id));
  const index = group.indexOf(change.id);
  const other = group[index + change.by];
  if (index < 0 || other === undefined) return null;
  return change.by < 0 ? { kind: "move", id: change.id, target: other } : { kind: "move", id: other, target: change.id };
}

export function applyTileChange({ order = [], pinned = [] }, change, arranged) {
  const resolved = resolveTileChange({ pinned }, change, arranged);
  if (!resolved) return { order, pinned: [...pinned].sort() };
  const all = [...arranged, ...order.filter((id) => !arranged.includes(id))];
  const pins = new Set(pinned);
  const pinning = (id, on) => {
    const next = new Set(pins);
    if (on) next.add(id); else next.delete(id);
    return [...next].sort();
  };
  if (resolved.kind === "pin") return { order: all, pinned: pinning(resolved.id, resolved.on) };
  return { order: movingTile(resolved.id, resolved.target, all), pinned: pinning(resolved.id, pins.has(resolved.target)) };
}
