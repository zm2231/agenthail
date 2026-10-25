import { requireDisplay } from "./session-display.js";
import { responseError } from "./http.js";

export function requirePosition(position) {
  if (!position || !position.hostId || !position.epoch || !Number.isSafeInteger(position.sequence)) throw Error("Missing daemon presentation position");
  return position;
}

export function olderPosition(candidate, previous) {
  return previous && candidate.hostId === previous.hostId && candidate.epoch === previous.epoch && candidate.sequence < previous.sequence;
}

export function requireSessionView(page) {
  const view = page.view;
  requirePosition(page.presentationPosition);
  if (!view || !view.catalogHostId || !view.revision || !Array.isArray(view.families) || !Array.isArray(view.projects) || !Array.isArray(view.surfaces) || !view.summary || !Array.isArray(page.sessions)) throw Error("Missing daemon session view");
  for (const scope of ["running", "current", "recent", "all", "archived"]) {
    if (!Number.isInteger(view.counts?.[scope]?.families) || !Number.isInteger(view.counts[scope].sessions)) throw Error("Missing daemon view counts");
  }
  if (view.refreshAt !== null && !Number.isFinite(Date.parse(view.refreshAt))) throw Error("Invalid view refresh time");
  page.sessions.forEach(requireDisplay);
  return page;
}

export async function readSessionView(parameters) {
  for (let attempt = 0; attempt < 3; attempt++) {
    try { return await readViewPages(parameters); }
    catch (error) { if (error.status !== 409 || attempt === 2) throw error; }
  }
}

async function readViewPages(parameters) {
  const base = new URLSearchParams(parameters);
  base.set("limit", "200");
  const fetchPage = async (params) => {
    const response = await fetch(`/api/state?${params}`);
    if (!response.ok) { const error = await responseError(response); error.status = response.status; throw error; }
    return requireSessionView(await response.json());
  };
  const result = await fetchPage(base);
  const families = [...result.view.families];
  const rows = new Map(result.sessions.map((row) => [row.id, row]));
  const cursors = new Set();
  const accept = (page) => {
    if (page.view.revision !== result.view.revision || page.presentationPosition.hostId !== result.presentationPosition.hostId || page.presentationPosition.epoch !== result.presentationPosition.epoch) throw Object.assign(Error("The catalog changed"), { status: 409 });
    page.sessions.forEach((row) => rows.set(row.id, row));
    result.presentationPosition = page.presentationPosition;
  };
  let cursor = result.nextCursor;
  while (cursor) {
    if (cursors.has(cursor)) throw Error("Repeated session view cursor");
    cursors.add(cursor);
    const params = new URLSearchParams(base);
    params.set("cursor", cursor);
    const page = await fetchPage(params);
    accept(page);
    families.push(...page.view.families);
    cursor = page.nextCursor;
  }
  const missing = [...new Set(families.flatMap((family) => family.members.map((member) => member.id)))].filter((id) => !rows.has(id));
  for (let offset = 0; offset < missing.length; offset += 200) {
    const params = new URLSearchParams(base);
    params.set("revision", result.view.revision);
    missing.slice(offset, offset + 200).forEach((id) => params.append("sessionId", id));
    accept(await fetchPage(params));
  }
  result.view = { ...result.view, families };
  result.sessions = [...rows.values()];
  delete result.nextCursor;
  for (const family of families) for (const member of family.members) if (!rows.has(member.id)) throw Error("Missing session view member");
  return result;
}

export function viewSessions(snapshot, familyIDs) {
  if (!snapshot) return [];
  const rows = new Map(snapshot.sessions.map((row) => [row.id, row]));
  const included = familyIDs && new Set(familyIDs);
  return snapshot.view.families.filter((family) => !included || included.has(family.id)).flatMap((family) => family.members.map((member) => rows.get(member.id)));
}
