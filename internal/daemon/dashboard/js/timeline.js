import { toolBlocks } from "./tool-diffs.js";
import { tools } from "./tool-loader.js";
import { renderToolRun } from "./tool-runs.js";
import { toolKinds } from "./tool-view.js";
import { isInstructions, renderTimelineItem, timelineItemAttributes } from "./timeline-items.js";
import { escape, turnFooter } from "./format.js";

export function renderTimeline(items, session) {
  toolBlocks.clear();
  const out = [];
  let run = [];
  const states = tools.toolCallStates(items, session.status === "busy" || Boolean(session.waitingOn));
  const flush = () => {
    if (run.length) out.push(renderToolRun(run, session, states));
    run = [];
  };
  for (let index = 0; index < items.length; index += 1) {
    const item = items[index];
    if (toolKinds.includes(item.kind)) {
      run.push(item);
      continue;
    }
    flush();
    if (item.kind === "event" && item.title === "Turn duration") {
      out.push(`<p class="timeline-item timeline-annotation" ${timelineItemAttributes(item)}>${escape(turnFooter(item.text, item.timestamp))}</p>`);
      continue;
    }
    const next = items[index + 1];
    const ends = next?.kind === "event" && next.title === "Turn duration";
    out.push(renderTimelineItem(item, session, ends ? next : null));
    if (ends && ["text", "message", "assistant"].includes(item.kind) && item.role !== "user" && item.role !== "peer" && !isInstructions(item)) index += 1;
  }
  flush();
  return out.join("");
}

export function timelineSignature(session, exchanges, goal, capabilities, transcriptWarning, timeline, claudeRuns, claudeSubagents, claudeSubagentsTotal, allSubagents) {
  return JSON.stringify([
    session.id,
    session.status === "busy" || Boolean(session.waitingOn),
    exchanges,
    JSON.stringify(goal || null),
    Boolean(capabilities.goal),
    Boolean(capabilities.goalSet),
    Boolean(capabilities.goalStatus),
    transcriptWarning,
    timeline,
    claudeRuns,
    claudeSubagents,
    claudeSubagentsTotal,
    allSubagents,
  ]);
}
