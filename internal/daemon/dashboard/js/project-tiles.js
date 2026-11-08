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

export function orderTiles(projects, limit) {
  const open = projects.filter((project) => !project.archived && ((project.sessionCount || 0) > 0 || (project.coordinators || []).length > 0));
  const recent = open.sort((a, b) => (Date.parse(b.lastActive || "") || 0) - (Date.parse(a.lastActive || "") || 0) || byName(a, b));
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
