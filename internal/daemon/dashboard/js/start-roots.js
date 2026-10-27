import { $ } from "./dom.js";
import { escape } from "./format.js";
import { syncStartForm } from "./start-form.js";
import { startHostQuery } from "./start-host.js";
import { app } from "./state.js";
import { pickedTitle, rootTitles, rootUsage, shortReason } from "./start-root-labels.js";

export function renderStartConfigRoots(roots, pick = null) {
  const select = $("#new-conversation-config-root");
  const current = select.value;
  const titles = rootTitles(roots, app.startOptions?.host?.name);
  select.innerHTML = [`<option value="">${escape(automaticOption(pick, roots, titles))}</option>`, ...roots.map((root, index) =>
    `<option value="${escape(root.root)}"${root.signedInAs || root.login === "api_key" || root.login === "proxy" ? "" : " disabled"}>${escape(claudeConfigRootOption(root, titles[index]))}</option>`,
  )].join("");
  if (roots.some(root => root.root === current)) select.value = current;
  select.closest("[data-claude-root]").dataset.single = roots.length < 2 ? "true" : "";
  syncStartRootDetail(pick);
  syncStartForm();
}

export function syncStartRootDetail(pick = app.startOptions?.claudePick) {
  const roots = app.startOptions?.claudeConfigRoots || [];
  const automatic = pick && $("#new-conversation-config-root").value === "";
  const name = automatic ? pickedTitle(pick, roots, rootTitles(roots, app.startOptions?.host?.name)) : "";
  $("#new-conversation-config-root-detail").textContent = automatic ? `Automatic picks ${name}: ${pick.reason}` : "";
}

export function automaticOption(pick, roots = [], titles = []) {
  if (!pick) return "Automatic";
  return `Automatic (${pickedTitle(pick, roots, titles)}, ${shortReason(pick.reason)})`;
}

export function loadStartRootPick(options, host) {
  if ((options.claudeConfigRoots || []).length < 2) return;
  fetch(`/api/v1/session-options/claude-pick${startHostQuery("?")}`)
    .then(response => response.ok ? response.json() : null)
    .then(answer => {
      if (!answer || host !== app.startHost || app.startOptions !== options) return;
      options.claudePick = answer.pick;
      options.claudePolicy = answer.policy;
      renderStartConfigRoots(options.claudeConfigRoots, options.claudePick);
    })
    .catch(() => {});
}

export function loadStartRootUsage(options, host) {
  for (const root of options.claudeConfigRoots || []) {
    if (!root.signedInAs && !root.proxy) continue;
    fetch(`/api/v1/session-options/claude-usage?root=${encodeURIComponent(root.root)}${startHostQuery("&")}`)
      .then(response => response.ok ? response.json() : null)
      .then(answer => {
        if (!answer || host !== app.startHost || app.startOptions !== options) return;
        root.usage = answer.usage;
        root.best = answer.best || null;
        renderStartConfigRoots(options.claudeConfigRoots, options.claudePick);
      })
      .catch(() => {});
  }
}

export function claudeConfigRootOption(root, title = root.name) {
  const exhausted = claudeUsageExhausted(root);
  const detail = exhausted
    ? `out of usage until ${new Date(exhausted).toLocaleString([], { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" })}`
    : rootUsage(root, app.startOptions?.claudePolicy);
  return [title, root.best ? `best: ${root.best.split("@")[0]}` : null, detail].filter(Boolean).join(" · ");
}

export function claudeUsageExhausted(root) {
  const now = Date.now();
  const resets = (root.usage?.limits || []).filter(limit => limit.percent >= 100 && Date.parse(limit.resetsAt) > now).map(limit => Date.parse(limit.resetsAt));
  return resets.length ? Math.max(...resets) : 0;
}
