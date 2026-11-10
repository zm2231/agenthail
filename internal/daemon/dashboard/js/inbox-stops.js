import { needsYouByProject } from "./project-tiles.js";

const hostOf = (item) => item.hostID ?? item.hostId ?? "";

export function inboxStops({ replacements = [], decisions = [] }) {
  const stopped = [
    ...replacements.map((item) => ({ kind: "replacement", id: `replacement:${hostOf(item)}/${item.id}`, item })),
    ...decisions.filter((item) => item.group === "stopped").map((item) => ({ kind: "decision", id: `decision:${item.sessionId}`, item })),
  ];
  return { stopped, decisions, count: stopped.length + decisions.filter((item) => item.group !== "stopped").length };
}

export function stopsByProject(stops, projects, projectOfSession) {
  const placed = stops.stopped.filter((stop) => stop.kind !== "decision").map(({ kind, item }) => ({
    sessionId: item.sessionId,
    projectId: kind === "replacement" ? item.projectId : projectOfSession(item.sessionId),
    hostId: hostOf(item),
  }));
  return needsYouByProject([...placed, ...stops.decisions], projects);
}
