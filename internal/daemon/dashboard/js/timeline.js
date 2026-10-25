import { toolBlocks } from "./tool-diffs.js";
import { tools } from "./tool-loader.js";
import { renderToolRun } from "./tool-runs.js";
import { toolKinds } from "./tool-view.js";
import { isInstructions, renderTimelineItem, timelineItemAttributes } from "./timeline-items.js";
import { escape, turnFooter } from "./format.js";
import { foldAgentEntries, isIncoming, isSend, renderAgentMessage, renderAgentSend } from "./agent-messages.js";

export function renderTimeline(items, session, expandAgents = false) {
  toolBlocks.clear();
  const out = [];
  let run = [];
  const states = tools.toolCallStates(items, session.status === "busy" || Boolean(session.waitingOn));
  const sends = new Map(items.filter((item) => isSend(item.agent) && item.callId).map((item) => [item.callId, null]));
  for (const item of items) {
    if (item.kind === "toolResult" && sends.has(item.callId) && !sends.get(item.callId)) sends.set(item.callId, item);
  }
  const sendResults = new Set([...sends.values()].filter(Boolean));
  const flush = () => {
    if (run.length) out.push({ html: renderToolRun(run, session, states) });
    run = [];
  };
  for (let index = 0; index < items.length; index += 1) {
    const item = items[index];
    if (sendResults.has(item)) continue;
    if (isIncoming(item.agent)) {
      flush();
      out.push({ html: renderAgentMessage(item, session, expandAgents), agent: item.agent, id: item.id });
      continue;
    }
    if (isSend(item.agent)) {
      flush();
      const html = renderAgentSend(item, sends.get(item.callId), session, expandAgents);
      const said = out.at(-1)?.sends;
      if (said) said.push(html);
      else out.push({ html });
      continue;
    }
    if (toolKinds.includes(item.kind)) {
      run.push(item);
      continue;
    }
    flush();
    if (item.kind === "event" && item.title === "Turn duration") {
      out.push({ html: `<p class="timeline-item timeline-annotation" ${timelineItemAttributes(item)}>${escape(turnFooter(item.text, item.timestamp))}</p>` });
      continue;
    }
    if (item.kind === "done") continue;
    const next = items[index + 1];
    const ends = next?.kind === "event" && next.title === "Turn duration";
    const reply = ["text", "message", "assistant"].includes(item.kind) && item.role !== "user" && !isInstructions(item);
    const turnEnd = ends ? next : null;
    out.push(reply ? { reply: item, turnEnd, sends: [] } : { html: renderTimelineItem(item, session, turnEnd) });
    if (ends && reply) index += 1;
  }
  flush();
  const entries = out.map((entry) => (entry.reply ? { html: renderTimelineItem(entry.reply, session, entry.turnEnd, entry.sends.join("")) } : entry));
  return (expandAgents ? entries.map((entry) => entry.html) : foldAgentEntries(entries, session)).join("");
}

export function timelineSignature(session, exchanges, goal, capabilities, transcriptWarning, timeline, claudeRuns, claudeSubagents, claudeSubagentsTotal, allSubagents, expandAgents) {
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
    Boolean(expandAgents),
  ]);
}
