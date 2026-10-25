import { clockTime, escape } from "./format.js";
import { markdown } from "./markdown.js";
import { renderAttachedFiles } from "./messages.js";
import { isFailedTool, toolRowKey, toolRows } from "./tool-view.js";

export const expandAgentsKey = "expandAgentMessages";

export function expandAgentsPreference() {
  return globalThis.localStorage?.getItem(expandAgentsKey) === "1";
}

export function setExpandAgentsPreference(on) {
  globalThis.localStorage?.setItem(expandAgentsKey, on ? "1" : "0");
}

const nouns = {
  peer: ["agent message", "agent messages"],
  task: ["task notice", "task notices"],
  continuation: ["automatic continuation", "automatic continuations"],
  notice: ["Agenthail notice", "Agenthail notices"],
};

export function isIncoming(agent) {
  return agent?.direction === "in";
}

export function isSend(agent) {
  return agent?.direction === "out" && agent.kind === "send";
}

export function agentChip(agent) {
  const name = agent.peer || "another agent";
  return agent.kind === "peer" && agent.channel ? `${name} · #${agent.channel}` : name;
}

export function agentRuns(agents) {
  const runs = [];
  let start = null;
  agents.forEach((agent, index) => {
    if (isIncoming(agent)) {
      if (start === null) start = index;
      return;
    }
    if (start !== null && index - start >= 2) runs.push([start, index]);
    start = null;
  });
  return runs;
}

export function agentFoldLabel(agents) {
  const count = agents.length;
  if (agents.every((agent) => agent.kind === "peer")) {
    const senders = new Set(agents.map(agentChip));
    return senders.size === 1 ? `${count} messages from ${[...senders][0]}` : `${count} messages from agents`;
  }
  const counts = new Map();
  for (const agent of agents) counts.set(agent.kind, (counts.get(agent.kind) || 0) + 1);
  const parts = [...counts].map(([kind, n]) => {
    const noun = nouns[kind] || ["update", "updates"];
    return `${n} ${n === 1 ? noun[0] : noun[1]}`;
  });
  return `${count} updates · ${parts.join(", ")}`;
}

export function agentFoldSenders(agents) {
  return [...new Set(agents.map(agentChip))].join(", ");
}

function rowOpen(key, expandAll) {
  return expandAll || (toolRows.get(key) ?? false);
}

function itemChip(item) {
  const chip = agentChip(item.agent);
  return item.agent.kind === "task" && item.status ? `${chip} · ${item.status}` : chip;
}

export function renderAgentMessage(item, session, expandAll) {
  const key = toolRowKey(session, `agent:${item.id}`);
  const files = item.files || [];
  const chip = itemChip(item);
  const fileNote = files.length ? ` · ${files.length === 1 ? "1 file" : `${files.length} files`}` : "";
  const time = clockTime(item.timestamp);
  const body = item.text ? `<div class="turn-content">${markdown(item.text)}</div>` : "";
  const copy = item.text ? `<button class="turn-action" type="button" data-copy-text="${escape(item.text)}">Copy</button>` : "";
  const footer = time || copy ? `<footer class="turn-footer">${time ? `<span class="turn-status">${escape(time)}</span>` : ""}<span class="turn-actions">${copy}</span></footer>` : "";
  const attributes = `data-timeline-item="${escape(item.id || "")}" data-row="${escape(key)}"${files.length ? " data-files" : ""}${expandAll ? " data-locked" : ""}`;
  return `<details class="timeline-item agent-message" ${attributes}${rowOpen(key, expandAll) ? " open" : ""}><summary><span class="agent-mark" aria-hidden="true">›</span><span class="agent-preview"><span class="agent-chip">${escape(chip)}</span> <em class="agent-summary">${escape(item.agent.summary)}</em><span class="agent-files">${escape(fileNote)}</span></span><span class="agent-more"></span></summary><div class="agent-body">${body}${renderAttachedFiles(files)}${item.truncated ? '<div class="timeline-meta">Shortened by the host</div>' : ""}${footer}</div></details>`;
}

export function renderAgentSend(call, result, session, expandAll) {
  const agent = call.agent;
  const failed = result && isFailedTool(result) ? '<span class="tool-failed">failed</span>' : "";
  const line = `<span class="agent-send-mark" aria-hidden="true">↳</span><span class="agent-send-line"><em>“${escape(agent.summary)}”</em> → <strong>${escape(agentChip(agent))}</strong></span>${failed}`;
  const attributes = `data-timeline-item="${escape(call.id || "")}"${call.callId ? ` data-call-id="${escape(call.callId)}"` : ""}`;
  if (!agent.body) return `<p class="timeline-item agent-send" ${attributes}>${line}</p>`;
  const key = toolRowKey(session, `agent:${call.id}`);
  return `<details class="timeline-item agent-send" ${attributes} data-row="${escape(key)}"${expandAll ? " data-locked" : ""}${rowOpen(key, expandAll) ? " open" : ""}><summary>${line}</summary><div class="agent-send-body">${escape(agent.body)}</div></details>`;
}

export function renderAgentFold(members, session) {
  const agents = members.map((member) => member.agent);
  const first = members[0].id;
  const key = toolRowKey(session, `agents:${first}`);
  const label = agentFoldLabel(agents);
  return `<details class="timeline-item agent-fold" data-row="${escape(key)}"${toolRows.get(key) ? " open" : ""}><summary><span class="agent-mark" aria-hidden="true">›</span><strong class="agent-fold-label">${escape(label)}</strong><span class="agent-fold-senders">${escape(agentFoldSenders(agents))}</span></summary><div class="agent-fold-members">${members.map((member) => member.html).join("")}</div></details>`;
}

export function foldAgentEntries(entries, session) {
  const runs = agentRuns(entries.map((entry) => entry.agent));
  const out = [];
  let next = 0;
  for (const [start, end] of runs) {
    out.push(...entries.slice(next, start).map((entry) => entry.html));
    out.push(renderAgentFold(entries.slice(start, end), session));
    next = end;
  }
  out.push(...entries.slice(next).map((entry) => entry.html));
  return out;
}

export function markClampedAgentRows(root) {
  for (const row of root.querySelectorAll("details.agent-message:not([open])")) {
    const preview = row.querySelector(".agent-preview");
    const fits = !row.hasAttribute("data-files") && preview && preview.scrollHeight <= preview.clientHeight + 1;
    row.toggleAttribute("data-fits", Boolean(fits));
  }
}

export function holdAgentRow(event) {
  const summary = event.target.closest?.("details.agent-message > summary, details.agent-send > summary");
  const row = summary?.parentElement;
  if (row && (row.hasAttribute("data-locked") || (row.hasAttribute("data-fits") && !row.open))) event.preventDefault();
}
