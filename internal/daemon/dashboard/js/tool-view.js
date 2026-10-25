import { escape } from "./format.js";
import { renderTimelineMetadata, timelineItemAttributes } from "./timeline-items.js";
import { tools } from "./tool-loader.js";
import { app } from "./state.js";
import { $ } from "./dom.js";

export const toolKinds = ["toolCall", "toolResult", "reasoning", "tool", "command"];

export const foldToolsAfter = 6;

export function isFailedTool(item) {
  return item.status === "failed" || item.status === "error";
}

export const toolRows = new Map();

export function toolRowKey(session, id) {
  return `${session?.id || ""} ${id}`;
}

export function renderToolLine(line, failed, attributes, body, key, open, collapse = "") {
  const object = line.object ? `<span class="tool-object${line.code ? " code" : ""}">${escape(line.object)}</span>` : "";
  const end = collapse ? renderCollapse(collapse) : "";
  return `<details class="timeline-item tool-line${failed ? " failed" : ""}" ${attributes} data-row="${escape(key)}"${(toolRows.get(key) ?? open) ? " open" : ""}><summary><span class="tool-mark" aria-hidden="true">◆</span><span class="tool-verb">${escape(line.verb)}</span>${object}${failed ? '<span class="tool-failed">failed</span>' : ""}</summary><div class="tool-body">${body}${end}</div></details>`;
}

export function renderCollapse(title) {
  return `<button class="tool-collapse" type="button" data-collapse-row>${escape(title)}</button>`;
}

export function renderToolOutput(result, session) {
  const metadata = renderTimelineMetadata(result);
  const failed = isFailedTool(result);
  const key = toolRowKey(session, `output:${result.id}`);
  const text = result.text || "";
  const lines = tools.outputLines(text);
  const preview = tools.outputPreview(text);
  const shown = preview.length < text.length ? Math.min(toolRows.get(key) || 0, lines.length) : 0;
  const more = shown < lines.length ? `<button class="tool-control" type="button" data-output-show="${shown + tools.outputPage}">${shown ? `Show ${Math.min(tools.outputPage, lines.length - shown)} more lines` : `Show all ${lines.length} lines`}</button>` : "";
  const paging = preview.length < text.length ? more + (shown ? '<button class="tool-control" type="button" data-output-show="0">Show less</button>' : "") : "";
  const copy = text ? `<button class="tool-control" type="button" data-copy-text="${escape(text)}">Copy output</button>` : "";
  const body = text ? `<pre class="tool-output${failed ? " failed" : ""}${shown ? " paged" : ""}">${escape(shown ? lines.slice(0, shown).join("\n") : preview)}</pre>` : '<p class="timeline-empty">The tool returned an empty result.</p>';

  const end = shown ? `<div class="tool-result-controls">${more}<button class="tool-control" type="button" data-output-show="0" data-reveal>Show less</button></div>` : "";
  return `<div class="tool-result" ${timelineItemAttributes(result)} data-row="${escape(key)}"><div class="tool-result-head"><span class="tool-result-label">${failed ? "Error output" : "Output"}</span>${paging}${copy}</div>${body}${end}${metadata}</div>`;
}

export function showToolOutput(button) {
  const block = button.closest(".tool-result[data-row]");
  const result = (app.history?.timeline?.items || []).find((item) => item.id === block?.dataset.timelineItem);
  if (!block || !result) return;
  toolRows.set(block.dataset.row, Number(button.dataset.outputShow) || 0);
  const reveal = button.hasAttribute("data-reveal");
  block.outerHTML = renderToolOutput(result, app.selected);
  if (reveal) $("#chat-body").querySelector(`.tool-result[data-row="${CSS.escape(block.dataset.row)}"]`)?.scrollIntoView({ block: "nearest" });
}

export function collapseToolRow(button) {
  const row = button.closest("details[data-row]");
  if (!row) return;
  row.open = false;
  toolRows.set(row.dataset.row, false);
  row.querySelector(":scope > summary")?.scrollIntoView({ block: "nearest" });
}

export function rememberToolRow(event) {
  const row = event.target;
  if (row.matches?.("details[data-row]")) toolRows.set(row.dataset.row, row.open);
}
