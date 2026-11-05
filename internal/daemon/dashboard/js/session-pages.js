import { responseError } from "./http.js";

const viewPath = (scope, params) => `/api/state?${new URLSearchParams({ scope, members: "1", ...params })}`;

async function readJSON(path) {
  const response = await fetch(path);
  if (!response.ok) throw await responseError(response);
  return response.json();
}

export function readBase(fresh = false) {
  return readJSON(`/api/state?sessions=none${fresh ? "&fresh=1" : ""}`);
}

export async function readHistory() {
  return (await readJSON("/api/v1/history?limit=50")).items || [];
}

export async function readListing({ scope, params, wanted = Infinity }) {
  const listing = { scope, params: JSON.stringify(params), view: null, counts: {}, hostEpoch: "", families: [], rows: new Map(), cursor: "", shown: 0 };
  const seen = new Set();
  const limit = String(Math.min(200, wanted));
  do {
    const page = await readJSON(viewPath(scope, { ...params, limit, ...(listing.cursor ? { cursor: listing.cursor } : {}) }));
    if (!listing.view) Object.assign(listing, { view: page.view, counts: page.counts || {}, hostEpoch: page.hostEpoch || "" });
    for (const family of page.view.families) {
      if (seen.has(family.id)) continue;
      seen.add(family.id);
      listing.families.push(family);
      listing.shown += family.members.filter((member) => !member.contextOnly).length;
    }
    for (const session of page.sessions || []) listing.rows.set(session.id, session);
    listing.cursor = page.nextCursor || "";
  } while (listing.cursor && listing.shown < wanted);
  return listing;
}

const hydrate = (scope, revision, ids) => {
  const query = new URLSearchParams({ scope, members: "1", revision });
  ids.forEach((id) => query.append("sessionId", id));
  return fetch(`/api/state?${query}`);
};

async function membersOf(scope, ids, found) {
  for (let attempt = 0; attempt < 3; attempt++) {
    const { view } = await readJSON(viewPath(scope, { limit: "1" }));
    const wanted = ids.filter((id) => !found.has(id));
    const batches = wanted.length > 1 ? [wanted, ...wanted.map((id) => [id])] : [wanted];
    let changed = false;
    for (const batch of batches) {
      if (batch.every((id) => found.has(id))) continue;
      const response = await hydrate(scope, view.revision, batch);
      if (response.ok) {
        for (const row of (await response.json()).sessions || []) found.set(row.id, row);
      } else if (response.status === 409) {
        changed = true;
        break;
      } else if (response.status !== 400) throw await responseError(response);
    }
    if (!changed) return;
  }
  throw new Error("The session list kept changing. Try again.");
}

export async function findSessions(ids) {
  const found = new Map();
  for (const scope of ["all", "archived"]) {
    if (ids.every((id) => found.has(id))) break;
    await membersOf(scope, ids, found);
  }
  return found;
}

export async function findSession(id) {
  return (await findSessions([id])).get(id) || null;
}
