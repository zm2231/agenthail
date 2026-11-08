import { needsYouByProject } from "./project-tiles.js";

const hostOf = (item) => item.hostID ?? item.hostId ?? "";

export function inboxStops({ attention = [], deliveries = [], replacements = [], decisions = [], localHosts = [""] }) {
  const local = new Set(localHosts);
  const shown = decisions.filter((item) => !(item.group === "stopped" && (item.kind === "incoming" || item.kind === "outgoing") && local.has(item.hostId ?? "")));
  const stopped = [
    ...attention.map((item) => ({ kind: "queue", id: `queue:${hostOf(item)}/${item.queueId}`, item })),
    ...deliveries.map((item) => ({ kind: "delivery", id: `delivery:${hostOf(item)}/${item.deliveryId}`, item })),
    ...replacements.map((item) => ({ kind: "replacement", id: `replacement:${hostOf(item)}/${item.id}`, item })),
    ...shown.filter((item) => item.group === "stopped").map((item) => ({ kind: "decision", id: `decision:${item.sessionId}`, item })),
  ];
  return { stopped, decisions: shown, count: stopped.length + shown.filter((item) => item.group !== "stopped").length };
}

export function stopsByProject(stops, projects, projectOfSession) {
  const placed = stops.stopped.filter((stop) => stop.kind !== "decision").map(({ kind, item }) => ({
    sessionId: item.sessionId,
    projectId: kind === "replacement" ? item.projectId : projectOfSession(item.sessionId),
    hostId: hostOf(item),
  }));
  return needsYouByProject([...placed, ...stops.decisions], projects);
}
