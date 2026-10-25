import { $ } from "./dom.js";
import { escape } from "./format.js";
import { syncStartForm } from "./start-form.js";
import { startHostQuery } from "./start-host.js";
import { app } from "./state.js";

export function renderStartConfigRoots(roots) {
  const select = $("#new-conversation-config-root");
  const current = select.value;
  select.innerHTML = roots.map(root =>
    `<option value="${root.primary ? "" : escape(root.root)}"${root.signedInAs ? "" : " disabled"}>${escape(claudeConfigRootOption(root))}</option>`,
  ).join("");
  if (roots.some(root => (root.primary ? "" : root.root) === current)) select.value = current;
  select.closest("[data-claude-root]").dataset.single = roots.length < 2 ? "true" : "";
  syncStartForm();
}

export function loadStartRootUsage(options, host) {
  for (const root of options.claudeConfigRoots || []) {
    if (!root.signedInAs) continue;
    fetch(`/api/v1/session-options/claude-usage?root=${encodeURIComponent(root.root)}${startHostQuery("&")}`)
      .then(response => response.ok ? response.json() : null)
      .then(answer => {
        if (!answer || host !== app.startHost || app.startOptions !== options) return;
        root.usage = answer.usage;
        renderStartConfigRoots(options.claudeConfigRoots);
      })
      .catch(() => {});
  }
}

export function claudeConfigRootOption(root) {
  const parts = [root.name, root.signedInAs ? `signed in as ${root.signedInAs.email}` : "signed out"];
  const exhausted = claudeUsageExhausted(root);
  if (exhausted) parts.push(`out of usage until ${new Date(exhausted).toLocaleString([], { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" })}`);
  return parts.join(" · ");
}

export function claudeUsageExhausted(root) {
  const now = Date.now();
  const resets = (root.usage?.limits || []).filter(limit => limit.percent >= 100 && Date.parse(limit.resetsAt) > now).map(limit => Date.parse(limit.resetsAt));
  return resets.length ? Math.max(...resets) : 0;
}
