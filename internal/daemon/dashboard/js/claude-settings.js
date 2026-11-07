import { responseError } from "./http.js";
import { app } from "./state.js";
import { $ } from "./dom.js";
import { escape, timeAgo } from "./format.js";
import { toast } from "./feedback.js";

export async function loadClaudeCompact() {
  try {
    const response = await fetch("/api/claude/compact-settings");
    if (!response.ok) throw await responseError(response);
    const body = await response.json();
    app.claudeCompact = body.roots || [];
    app.claudeCompactHolds = body.idleCompactionHolds || [];
    renderClaudeCompact();
  } catch (error) {
    $("#claude-compact-roots").innerHTML = `<div class="empty-card">${escape(error.message)}</div>`;
  }
}

export function claudeCompactWindowLabel(window) {
  if (!window) return "Claude's default";
  if (window.auto) return "auto";
  if (window.ignored) return `${window.ignored} (Claude ignores it)`;
  return `${Number(window.tokens).toLocaleString()} tokens`;
}

export function claudeSettingLabel(value) {
  return value === undefined || value === null ? "not set" : value ? "on" : "off";
}

function agenthailIdleLabel(idle) {
  if (!idle || !idle.enabled) return "off";
  return `on, from ${Number(idle.minContextTokens).toLocaleString()} tokens`;
}

const idleHoldsShown = 5;

function idleHoldLines(root) {
  return (app.claudeCompactHolds || []).filter((hold) => hold.root === root.root).slice(-idleHoldsShown).reverse()
    .map((hold) => `<p>Not compacted: ${escape(hold.session || hold.sessionId)} at ${Number(hold.contextTokens).toLocaleString()} tokens, ${escape(timeAgo(hold.at))}: ${escape(hold.reason)}</p>`).join("");
}

export function renderClaudeCompact() {
  const roots = app.claudeCompact || [];
  $("#claude-compact-roots").innerHTML = roots.map((root) => {
    const models = Object.entries(root.modelWindows || {}).map(([model, window]) => `<p>${escape(model)}: ${escape(claudeCompactWindowLabel(window))}</p>`).join("");
    const overrides = Object.entries(root.overrides || {}).map(([name, value]) => `<p>${escape(name)}=${escape(value)} is set where Agenthail runs and takes precedence over this file.</p>`).join("");
    return `<article class="setting-row"><div><strong>${escape(root.settingsPath)}</strong><p>Window: ${escape(claudeCompactWindowLabel(root.autoCompactWindow))}</p>${models}<p>Autocompact: ${escape(claudeSettingLabel(root.autoCompactEnabled))} · Idle compaction: ${escape(claudeSettingLabel(root.idleCompaction))}</p><p>Agenthail idle compaction: ${escape(agenthailIdleLabel(root.agenthailIdleCompaction))}</p>${idleHoldLines(root)}${overrides}</div></article>`;
  }).join("") || '<div class="empty-card">No Claude config folders.</div>';
  const select = $("#claude-compact-root");
  const chosen = select.value;
  select.innerHTML = roots.map((root) => `<option value="${escape(root.root)}">${escape(root.root)}</option>`).join("");
  if (roots.some((root) => root.root === chosen)) select.value = chosen;
}

export function bindClaudeSettingsEvents() {
  $("#claude-compact-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    const body = { root: $("#claude-compact-root").value, model: $("#claude-compact-model").value.trim(), window: $("#claude-compact-window").value.trim() };
    for (const [key, id] of [["enabled", "#claude-compact-enabled"], ["idleCompaction", "#claude-compact-idle"], ["agenthailIdleCompaction", "#claude-compact-agenthail-idle"]]) {
      if ($(id).value) body[key] = $(id).value === "on";
    }
    const idleMin = $("#claude-compact-agenthail-idle-min").value.trim();
    if (idleMin) body.agenthailIdleMinTokens = Number(idleMin);
    try {
      const response = await fetch("/api/claude/compact-settings", { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(body) });
      if (!response.ok) throw await responseError(response);
      await loadClaudeCompact();
      toast("Saved.");
    } catch (error) {
      toast(error.message);
    }
  });
}
