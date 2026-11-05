import { app } from "./state.js";
import { responseError } from "./http.js";

const orderScopes = ["current", "all", "archived"];

async function readScope(scope) {
  const view = { projects: [], families: [], refreshAt: null };
  const seen = new Set();
  let cursor = "";
  do {
    const response = await fetch(`/api/state?scope=${scope}&limit=200${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`);
    if (!response.ok) throw await responseError(response);
    const page = await response.json();
    if (!cursor) {
      view.projects = page.view.projects;
      view.refreshAt = page.view.refreshAt;
    }
    for (const family of page.view.families) {
      if (seen.has(family.id)) continue;
      seen.add(family.id);
      view.families.push(family);
    }
    cursor = page.nextCursor || "";
  } while (cursor);
  return view;
}

export async function loadSessionOrder() {
  const views = await Promise.all(orderScopes.map(readScope));
  app.sessionOrder = Object.fromEntries(orderScopes.map((scope, index) => [scope, views[index]]));
  return views.map((view) => view.refreshAt).filter(Boolean).map(Date.parse).reduce((first, at) => Math.min(first, at), Infinity);
}

function members(families) {
  const rows = new Map(app.state.sessions.map((session) => [session.id, session]));
  const listed = [];
  for (const family of families) {
    for (const member of family.members) {
      const row = !member.contextOnly && rows.get(member.id);
      if (row) listed.push(row);
    }
  }
  return listed;
}

function view(scope) {
  return app.sessionOrder?.[scope] || { projects: [], families: [] };
}

export function orderedSessions(scope) {
  return members(view(scope).families);
}

export function orderedProjectIDs(scope) {
  return view(scope).projects.map((project) => project.id);
}

export function projectSessions(scope, projectID, collapsed = false) {
  const shown = collapsed ? new Set(view(scope).projects.find((project) => project.id === projectID)?.collapsedFamilyIds || []) : null;
  return members(view(scope).families.filter((family) => family.projectId === projectID && (!shown || shown.has(family.id))));
}
