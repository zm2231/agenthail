import { escape, timeAgo } from "./format.js";

export const claudeSubagentsShown = 8;

export function claudeSubagentList(links, total, expanded) {
  const ordered = links
    .map((link, index) => ({ link, index }))
    .sort((a, b) => (Boolean(b.link.working) - Boolean(a.link.working)) || ((Date.parse(b.link.lastActive || "") || 0) - (Date.parse(a.link.lastActive || "") || 0)) || (a.index - b.index))
    .map(({ link }) => link);
  const working = ordered.filter((link) => link.working).length;
  const count = Math.max(Number.isInteger(total) ? total : ordered.length, ordered.length);
  const shown = expanded ? ordered : ordered.slice(0, working + claudeSubagentsShown);
  return { shown, working, total: count, earlier: count - shown.length };
}

export function claudeSubagentTitle(link) {
  return link.description || link.agentType || `Subagent ${String(link.agentId || "").slice(0, 8)}`;
}

export function renderClaudeMetadata(claudeRuns, claudeSubagents, claudeSubagentsTotal, allSubagents = false) {
  if (!Array.isArray(claudeRuns) && !Array.isArray(claudeSubagents)) return "";
  const runs = Array.isArray(claudeRuns) ? claudeRuns : [];
  const list = claudeSubagentList(Array.isArray(claudeSubagents) ? claudeSubagents : [], claudeSubagentsTotal, allSubagents);
  const runRows = runs.map((run) => `<li>${run.jobId ? `<span>Job: ${escape(run.jobId)}</span>` : ""}${run.runType ? ` · <span>Type: ${escape(run.runType)}</span>` : ""}${run.providerState ? ` · <span>Provider state: ${escape(run.providerState)}</span>` : ""}${run.sessionId ? ` · <span>Session: ${escape(run.sessionId)}</span>` : ""}${run.resumeSessionId ? ` · <span>Resume session: ${escape(run.resumeSessionId)}</span>` : ""}${run.createdAt ? ` · <time datetime="${escape(run.createdAt)}">Created ${escape(run.createdAt)}</time>` : ""}${run.updatedAt ? ` · <time datetime="${escape(run.updatedAt)}">Updated ${escape(run.updatedAt)}</time>` : ""}${run.recordPath ? ` · <span>Record: ${escape(run.recordPath)}</span>` : ""}</li>`).join("");
  const subagentRows = list.shown.map((link) => `<li><span>${escape(claudeSubagentTitle(link))}</span>${link.description && link.agentType ? ` · <span>${escape(link.agentType)}</span>` : ""}${link.working ? " · <span>Working</span>" : link.lastActive ? ` · <time datetime="${escape(link.lastActive)}">${escape(timeAgo(link.lastActive))}</time>` : ""}</li>`).join("");
  const noun = list.total === 1 ? "subagent" : "subagents";
  const summary = list.working ? `${list.working} working, ${list.total} ${noun} in all` : `${list.total} ${noun} in all`;
  const earlier = list.earlier > 0 ? `<button class="soft-button" data-subagents-all type="button">Show ${list.earlier} earlier</button>` : "";
  return `<details class="session-details"><summary>Observed Claude runs</summary>${runRows ? `<ul>${runRows}</ul>` : "<p>No observed Claude runs.</p>"}</details><details class="session-details"${allSubagents ? " open" : ""}><summary>Observed Claude subagents${list.total ? ` · ${escape(summary)}` : ""}</summary>${subagentRows ? `<ul>${subagentRows}</ul>${earlier}` : "<p>No observed Claude subagents.</p>"}</details>`;
}
