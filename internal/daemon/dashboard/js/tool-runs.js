import { renderTimelineMetadata, timelineItemAttributes } from "./timeline-items.js";
import { markdown } from "./markdown.js";
import { foldToolsAfter, isFailedTool, renderCollapse, renderToolLine, renderToolOutput, toolRowKey, toolRows } from "./tool-view.js";
import { tools } from "./tool-loader.js";
import { cachedToolDiffs, renderDiff, renderFileChips } from "./tool-diffs.js";
import { escape } from "./format.js";

export function renderToolRun(items, session, states = new Map()) {
  const entries = [];
  const calls = new Map();
  for (const item of items) {
    if (item.kind === "toolResult" && item.callId && calls.has(item.callId)) {
      calls.get(item.callId).results.push(item);
    } else if (item.kind === "toolResult" || item.kind === "reasoning") {
      entries.push({ item, results: [] });
    } else {
      const entry = { item, results: [] };
      if (item.callId) calls.set(item.callId, entry);
      entries.push(entry);
    }
  }
  const lines = entries.map(({ item, results }) => {
    const attributes = timelineItemAttributes(item);
    if (item.kind === "reasoning") {
      const body = item.text ? `<div class="turn-content">${markdown(item.text)}</div>` : '<p class="timeline-empty">No recorded content.</p>';
      return renderToolLine({ verb: "Thought", object: tools.firstLine(item.text), code: false }, false, attributes, body + renderTimelineMetadata(item), toolRowKey(session, item.id), false);
    }
    if (item.kind === "toolResult") {
      return renderToolLine({ verb: "Output", object: tools.firstLine(item.text), code: true }, isFailedTool(item), attributes, renderToolOutput(item, session), toolRowKey(session, item.id), isFailedTool(item));
    }
    const failed = isFailedTool(item) || results.some(isFailedTool);
    const diffs = cachedToolDiffs(item, session);
    const input = diffs.length
      ? diffs.map((diff) => `${diffs.length > 1 ? `<div class="diff-file">${escape(tools.baseName(diff.path))}</div>` : ""}${renderDiff(toolRowKey(session, `diff:${item.id}:${diff.path}`), diff.hunks)}`).join("")
      : item.text ? `<pre class="tool-input">${escape(tools.readableInput(item.text))}</pre>` : '<p class="timeline-empty">No recorded input.</p>';
    const outputs = results.length ? results.map((result) => renderToolOutput(result, session)).join("") : `<p class="timeline-empty">${escape(tools.callStateLabels[states.get(item.id) || "unrecorded"])}</p>`;
    return renderToolLine(tools.toolLine(item, diffs), failed, attributes, (failed ? "" : input + renderTimelineMetadata(item)) + outputs, toolRowKey(session, item.id), failed, "Collapse tool");
  });
  const diffs = tools.mergedDiffs(entries.flatMap(({ item, results }) => (item.kind === "reasoning" || item.kind === "toolResult" || isFailedTool(item) || results.some(isFailedTool) ? [] : cachedToolDiffs(item, session))));
  const chips = diffs.length ? renderFileChips(toolRowKey(session, `chips:${entries[0].item.id}`), diffs) : "";
  const callCount = entries.filter(entry => entry.item.kind !== "reasoning" && entry.item.kind !== "toolResult").length;
  if (callCount <= foldToolsAfter) return `<section class="tool-run">${lines.join("")}${chips}</section>`;
  const failures = entries.filter(({ item, results }) => isFailedTool(item) || results.some(isFailedTool)).length;
  const summary = `<summary><span class="tool-mark" aria-hidden="true">◆</span><span class="tool-verb">${escape(toolRunLabel(entries, session))}</span>${failures ? `<span class="tool-failed">${failures} failed</span>` : ""}</summary>`;
  const key = toolRowKey(session, `run:${entries[0].item.id}`);
  return `<section class="tool-run"><details class="tool-line tool-fold" data-row="${escape(key)}"${toolRows.get(key) ? " open" : ""}>${summary}<div class="tool-fold-lines">${lines.join("")}${renderCollapse("Collapse activity")}</div></details>${chips}</section>`;
}

export function toolRunLabel(entries, session) {
  const counts = new Map();
  for (const { item } of entries) {
    if (item.kind === "reasoning" || item.kind === "toolResult") continue;
    const verb = tools.toolLine(item, cachedToolDiffs(item, session)).verb;
    counts.set(verb, (counts.get(verb) || 0) + 1);
  }
  const phrases = { Run: ["ran 1 command", "ran %d commands"], Read: ["read 1 file", "read %d files"], Edit: ["edited 1 file", "edited %d files"], Write: ["wrote 1 file", "wrote %d files"], Search: ["searched once", "searched %d times"] };
  const parts = [...counts.entries()].sort((a, b) => b[1] - a[1]).map(([verb, count]) => {
    const phrase = phrases[verb];
    return phrase ? (count === 1 ? phrase[0] : phrase[1].replace("%d", count)) : `${verb} ×${count}`;
  });
  const text = parts.slice(0, 2).join(" and ") + (parts.length > 2 ? ` +${parts.length - 2} more` : "");
  return text.charAt(0).toUpperCase() + text.slice(1);
}
