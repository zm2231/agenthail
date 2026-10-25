import { compactTokenCount, escape } from "./format.js";

export function goalMetrics(goal) {
  return goal ? `<div class="session-tool-note">Status: ${escape(goalStatusLabel(goal.status))}${goal.timeUsedSeconds != null ? ` · Elapsed: ${escape(formatGoalDuration(goal.timeUsedSeconds))}` : ""}${goal.tokensUsed != null ? ` · Tokens: ${compactTokenCount(goal.tokensUsed)}` : ""}${goal.tokenBudget != null ? ` · Budget: ${compactTokenCount(goal.tokenBudget)}` : ""}${goal.createdAt ? ` · Created: ${escape(goal.createdAt)}` : ""}${goal.updatedAt ? ` · Updated: ${escape(goal.updatedAt)}` : ""}</div>${goal.reason ? `<p class="session-tool-note">Reason: ${escape(goal.reason)}</p>` : ""}` : "";
}

export function goalStatusLabel(status) {
  return ({ active: "Active", paused: "Paused", blocked: "Blocked", usageLimited: "Usage limited", budgetLimited: "Budget limited", complete: "Complete" })[status] || status || "Unknown";
}

export function renderGoalAttention(goal) {
  if (!goal || !["blocked", "usageLimited", "budgetLimited"].includes(goal.status)) return "";
  return `<p class="goal-attention" role="status">Needs you: ${escape(goalStatusLabel(goal.status))}. Review the goal in Conversation settings.</p>`;
}

export function formatGoalDuration(seconds) {
  const value = Number(seconds);
  if (!Number.isFinite(value)) return "Unknown";
  if (value < 60) return `${value}s`;
  const minutes = Math.floor(value / 60);
  if (minutes < 60) return `${minutes}m ${value % 60}s`;
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}
