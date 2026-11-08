import { app } from "./state.js";
import { readBase, readListing } from "./session-pages.js";

export const projectKey = (id, scope = "all") => `project:${scope}:${id}`;

export function scopeOrderKeys() {
  const id = app.scope;
  if (!id || id === "all" || id === "inbox") return [];
  const archived = (app.state?.projects || []).some((project) => project.id === id && project.archived);
  const scopes = archived ? ["archived"] : app.inboxMode === "current" ? ["current"] : app.showArchived ? ["all", "archived"] : ["all"];
  return scopes.map((scope) => `scope:${scope}:${id}`);
}

export function listOrderScopes() {
  if (app.inboxMode === "current") return ["current"];
  if (app.inboxMode === "projects") return ["all", "archived"];
  return app.showArchived ? ["all", "archived"] : ["all"];
}

export function neededOrderScopes() {
  const keys = new Set(["current"]);
  if (app.view === "conversations") {
    listOrderScopes().forEach((key) => keys.add(key));
    if (app.inboxMode === "projects") {
      for (const project of app.state?.projects || []) if (app.expandedProjects?.has(project.id)) keys.add(projectKey(project.id, project.archived ? "archived" : "all"));
    }
    if (app.selectedProject) keys.add(projectKey(app.selectedProject));
    scopeOrderKeys().forEach((key) => keys.add(key));
  }
  return [...keys];
}

function listParams() {
  const params = {};
  if (app.filters.surface !== "all") params.surface = app.filters.surface;
  if (app.filters.status !== "all") params.status = app.filters.status;
  if (app.listQuery) params.q = app.listQuery;
  return params;
}

export function listingSpec(key) {
  if (key.startsWith("project:")) {
    const scope = key.split(":")[1];
    return { scope, params: { projectId: key.slice(`project:${scope}:`.length) } };
  }
  if (key.startsWith("scope:")) {
    const scope = key.split(":")[1];
    return { scope, params: { projectId: key.slice(`scope:${scope}:`.length), ...listParams() } };
  }
  if (key === "current") return { scope: key, params: {} };
  if (app.inboxMode === "projects") return { scope: key, params: { collapsed: "1", ...(app.listQuery ? { q: app.listQuery } : {}) } };
  return { scope: key, params: listParams(), wanted: app.sessionLimit || 1 };
}

export function orderReady(keys) {
  return keys.every((key) => app.sessionOrder?.[key]);
}

function stale(key) {
  const listing = app.sessionOrder?.[key];
  const spec = listingSpec(key);
  return !listing || listing.params !== JSON.stringify(spec.params) || Boolean(listing.cursor && listing.shown < (spec.wanted ?? Infinity));
}

export function orderIsMissing() {
  return neededOrderScopes().some(stale);
}

export async function loadSessionOrder(fresh = false) {
  for (let attempt = 0; attempt < 3; attempt++) {
    const keys = neededOrderScopes();
    const [base, listings] = await Promise.all([readBase(fresh), Promise.all(keys.map((key) => readListing(listingSpec(key))))]);
    if (!listings.every((listing) => !listing.mixed && listing.hostEpoch === base.hostEpoch)) continue;
    app.sessionOrder = Object.fromEntries(keys.map((key, index) => [key, listings[index]]));
    const refreshAt = listings.map((listing) => listing.view.refreshAt).filter(Boolean).map(Date.parse).reduce((first, at) => Math.min(first, at), Infinity);
    const catalogSeq = Math.min(base.catalogSeq ?? 0, ...listings.map((listing) => listing.catalogSeq));
    return { base, refreshAt, catalogSeq };
  }
  throw new Error("The computer restarted while the dashboard was reading it. Trying again.");
}

export function listedRows() {
  const rows = new Map();
  for (const listing of Object.values(app.sessionOrder || {})) for (const [id, row] of listing.rows) rows.set(id, row);
  return rows;
}

function members(listing, families) {
  const listed = [];
  for (const family of families) {
    for (const member of family.members) {
      const row = !member.contextOnly && listing.rows.get(member.id);
      if (row) listed.push(row);
    }
  }
  return listed;
}

const listing = (key) => app.sessionOrder?.[key] || { view: { projects: [] }, families: [], rows: new Map(), cursor: "" };

export function orderedSessions(key) {
  return members(listing(key), listing(key).families);
}

export function listingHasMore(key) {
  return Boolean(listing(key).cursor);
}

export function listedTotal(key) {
  return listing(key).view.counts?.[listing(key).scope]?.sessions;
}

export function listedProject(key, projectID) {
  return listing(key).view.projects.find((project) => project.id === projectID) || null;
}

export function orderedProjectIDs(key) {
  return listing(key).view.projects.map((project) => project.id);
}

export function projectSessions(key, projectID, collapsed = false) {
  const read = listing(key);
  const shown = collapsed ? new Set(read.view.projects.find((project) => project.id === projectID)?.collapsedFamilyIds || []) : null;
  return members(read, read.families.filter((family) => family.projectId === projectID && (!shown || shown.has(family.id))));
}
