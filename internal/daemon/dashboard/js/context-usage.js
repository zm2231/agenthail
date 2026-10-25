import { $ } from "./dom.js";
import { compactTokenCount } from "./format.js";

export function contextBreakdown(context) {
  const count = (value) => Number(value).toLocaleString();
  const window = context.windowEstimated ? "Estimated window" : context.contextWindowSource === "configured" ? "Configured window" : "Context window";
  const rows = [["Used tokens", count(context.usedTokens || 0)], [window, context.contextWindow > 0 ? count(context.contextWindow) : "Unavailable"]];
  for (const [label, key] of [["Input", "inputTokens"], ["Cached input", "cachedInputTokens"], ["Output", "outputTokens"], ["Reasoning output", "reasoningOutputTokens"], ["Cumulative tokens", "cumulativeTokens"]]) {
    if (Number.isFinite(context[key])) rows.push([label, count(context[key])]);
  }
  rows.push(["Compactions", count(context.compactionCount || 0)]);
  if (Number.isFinite(context.reclaimedTokens)) rows.push(["Tokens reclaimed", count(context.reclaimedTokens)]);
  return rows;
}

export function renderContextUsage(context) {
  const indicator = $("#context-usage");
  if (!context || (!context.contextWindow && !context.compactionCount && !context.usedTokens)) {
    indicator.hidden = true;
    indicator.textContent = "";
    indicator.className = "";
    return;
  }
  const hasWindow = context.contextWindow > 0 && context.usedTokens <= context.contextWindow;
  const percent = hasWindow
    ? Math.round((context.usedTokens / context.contextWindow) * 100)
    : 0;
  indicator.hidden = false;
  indicator.className = context.compacting
    ? "context-usage compacting"
    : percent >= 90
      ? "context-usage critical"
      : percent >= 80
        ? "context-usage warning"
        : "context-usage";
  indicator.textContent = context.compacting
    ? "Compacting context"
    : hasWindow
      ? `Context ${compactTokenCount(context.usedTokens)} / ${compactTokenCount(context.contextWindow)} · ${percent}%`
      : context.usedTokens
        ? `Context ${compactTokenCount(context.usedTokens)} tokens`
        : `${context.compactionCount} compaction${context.compactionCount === 1 ? "" : "s"}`;
  const details = [];
  if (context.windowEstimated) details.push("Claude context window is estimated");
  if (context.contextWindowSource === "unknown") details.push("Claude did not report a configured context window");
  if (context.compactionCount) details.push(`${context.compactionCount} compaction${context.compactionCount === 1 ? "" : "s"}`);
  if (context.preCompactTokens && context.postCompactTokens)
    details.push(`Last compact: ${compactTokenCount(context.preCompactTokens)} to ${compactTokenCount(context.postCompactTokens)}, ${compactTokenCount(context.reclaimedTokens)} reclaimed`);
  const breakdown = contextBreakdown(context).map(([label, value]) => `${label}: ${value}`).join("\n");
  indicator.title = details.length ? `${details.join(". ")}\n\n${breakdown}` : breakdown;
}
