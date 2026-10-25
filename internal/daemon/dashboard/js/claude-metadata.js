import { displayText } from "./session-display.js";
import { escape, timeAgo } from "./format.js";

export function claudeSubagentTitle(link) {
  return link.description || link.agentType || `Subagent ${String(link.agentId || "").slice(0, 8)}`;
}

export function renderClaudeMetadata(claudeRuns, claudeSubagents, claudeSubagentsTotal, claudeSubagentsWorking, claudeSubagentsRemaining, allSubagents = false) {
  if (!Array.isArray(claudeRuns) && !Array.isArray(claudeSubagents)) return "";
  const runs = Array.isArray(claudeRuns) ? claudeRuns : [];
  const list = { shown: claudeSubagents || [], total: claudeSubagentsTotal, working: claudeSubagentsWorking, earlier: claudeSubagentsRemaining };
  if (Array.isArray(claudeSubagents) && ![list.total, list.working, list.earlier].every(Number.isInteger)) throw Error("Missing subagent presentation counts");
  const runRows = runs.map((run) => `<li>${run.jobId ? `<span>Job: ${escape(run.jobId)}</span>` : ""}${run.runType ? ` · <span>Type: ${escape(run.runType)}</span>` : ""}${run.providerState ? ` · <span>Provider state: ${escape(run.providerState)}</span>` : ""}${run.sessionId ? ` · <span>Session: ${escape(run.sessionId)}</span>` : ""}${run.resumeSessionId ? ` · <span>Resume session: ${escape(run.resumeSessionId)}</span>` : ""}${run.createdAt ? ` · <time datetime="${escape(run.createdAt)}">Created ${escape(run.createdAt)}</time>` : ""}${run.updatedAt ? ` · <time datetime="${escape(run.updatedAt)}">Updated ${escape(run.updatedAt)}</time>` : ""}${run.recordPath ? ` · <span>Record: ${escape(run.recordPath)}</span>` : ""}</li>`).join("");
  const subagentRows = list.shown.map((link) => `<li><span>${escape(claudeSubagentTitle(link))}</span>${link.description && link.agentType ? ` · <span>${escape(link.agentType)}</span>` : ""} · <span>${escape(displayText(link))}</span></li>`).join("");
  const noun = list.total === 1 ? "subagent" : "subagents";
  const summary = list.working ? `${list.working} working, ${list.total} ${noun} in all` : `${list.total} ${noun} in all`;
  const earlier = list.earlier > 0 ? `<button class="soft-button" data-subagents-all type="button">Show ${list.earlier} earlier</button>` : "";
  return `<details class="session-details"><summary>Observed Claude runs</summary>${runRows ? `<ul>${runRows}</ul>` : "<p>No observed Claude runs.</p>"}</details><details class="session-details"${allSubagents ? " open" : ""}><summary>Observed Claude subagents${list.total ? ` · ${escape(summary)}` : ""}</summary>${subagentRows ? `<ul>${subagentRows}</ul>${earlier}` : "<p>No observed Claude subagents.</p>"}</details>`;
}
