import { app } from "./state.js";

export const decisionGroups = [
  ["stopped", "Stopped on you"],
  ["holding", "Holding up work"],
  ["waiting", "Waiting on you"],
];

export const decisionTimeout = 6000;
const separator = "::";

export function decisionHosts() {
  const local = app.state?.daemon?.host;
  const paired = app.state?.daemon?.hosts || [];
  return [{ id: "", name: local?.name || "this computer", state: "connected" }, ...paired.map((host) => ({ id: host.id, name: host.name, state: host.state }))];
}

export function qualify(hostId, id) {
  if (!hostId || !id || id.includes(separator)) return id;
  return `${hostId}${separator}${id}`;
}

function qualified(item, hostId) {
  const out = { ...item, hostId, sessionId: qualify(hostId, item.sessionId) };
  if (item.projectId) out.projectId = qualify(hostId, item.projectId);
  if (item.holdsUp) out.holdsUp = item.holdsUp.map((id) => qualify(hostId, id));
  if (item.heldBy) out.heldBy = qualify(hostId, item.heldBy);
  return out;
}

export function mergeDecisions(answers) {
  const rank = Object.fromEntries(decisionGroups.map(([group], index) => [group, index]));
  const inbox = { decisions: [], leadHandling: [], unreachable: [], outdated: [] };
  for (const answer of answers) {
    if (answer.outdated) inbox.outdated.push(answer.host.name);
    else if (answer.error) inbox.unreachable.push(answer.host.name);
    else {
      inbox.decisions.push(...(answer.page.decisions || []).map((item) => qualified(item, answer.host.id)));
      inbox.leadHandling.push(...(answer.page.leadHandling || []).map((item) => qualified(item, answer.host.id)));
    }
  }
  inbox.decisions.sort((a, b) => (rank[a.group] - rank[b.group]) || String(a.since || "").localeCompare(String(b.since || "")) || a.sessionId.localeCompare(b.sessionId));
  inbox.unreachable.sort();
  inbox.outdated.sort();
  return inbox;
}

async function ask(host, fetchImpl) {
  if (host.state === "incompatible") return { host, outdated: true };
  if (host.state !== "connected") return { host, error: host.state || "not connected" };
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), decisionTimeout);
  try {
    const response = await fetchImpl(`/api/v1/decisions${host.id ? `?host=${encodeURIComponent(host.id)}` : ""}`, { signal: controller.signal, headers: { accept: "application/json" } });
    if (response.status === 404) {
      const body = await response.json().catch(() => ({}));
      if (body?.error?.code === "unknown_route") return { host, outdated: true };
    }
    if (!response.ok) return { host, error: `HTTP ${response.status}` };
    return { host, page: await response.json() };
  } catch (error) {
    return { host, error: error.message || String(error) };
  } finally {
    clearTimeout(timer);
  }
}

export async function loadDecisions(fetchImpl = fetch) {
  return mergeDecisions(await Promise.all(decisionHosts().map((host) => ask(host, fetchImpl))));
}

export function decisionsKey() {
  const hosts = decisionHosts().map((host) => `${host.id}:${host.state}`).join(",");
  const catalog = app.state?.catalogSeq === undefined ? `updated:${app.state?.updatedAt || ""}` : `${app.state?.hostEpoch || ""}:${app.state.catalogSeq}`;
  return `${hosts}|${catalog}`;
}
