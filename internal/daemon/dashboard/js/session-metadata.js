import { app } from "./state.js";
import { renderChat } from "./chat.js";

export function loadSessionMetadata(id, detail) {
  const metadataSeq = detail.journalSeq;
  fetch(`/api/session-metadata?id=${encodeURIComponent(id)}${app.allSubagents ? "&subagents=all" : ""}`)
    .then((result) => { if (!result.ok) throw Error("Metadata unavailable"); return result.json(); })
    .then((metadata) => { if (app.selected?.id === id && app.history === detail) { applySessionMetadata(detail, metadata, metadataSeq); renderChat(); } })
    .catch(() => {});
}

export function applySessionMetadata(detail, metadata, initialSeq) {
  const {goal, context, ...other} = metadata;
  Object.assign(detail, other);
  if (detail.journalSeq === initialSeq) {
    if (Object.hasOwn(metadata, "goal")) detail.goal = goal;
    if (Object.hasOwn(metadata, "context")) detail.context = context;
  }
}
