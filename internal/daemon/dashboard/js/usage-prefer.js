const clock = (value) => new Date(value).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });

function el(document, tag, className, text) {
  const element = document.createElement(tag);
  if (className) element.className = className;
  if (text !== undefined) element.textContent = text;
  return element;
}

export function preferActions(document, account, prefer, peers = true) {
  const proxied = account.proxyAccount;
  const row = el(document, "div", "usage-prefer");
  const toggle = el(document, "button", proxied.preferred ? "usage-prefer-toggle on" : "usage-prefer-toggle", "Preferred");
  toggle.type = "button";
  toggle.setAttribute("aria-pressed", String(Boolean(proxied.preferred)));
  toggle.title = proxied.preferred ? `Stop preferring ${account.login}; other accounts are not changed` : `Prefer ${account.login}; other accounts are not changed`;
  toggle.addEventListener("click", () => prefer(account, proxied.preferred ? "off" : "on"));
  const only = el(document, "button", "usage-prefer-only", "Prefer only this");
  only.type = "button";
  only.title = `Prefer ${account.login} and turn off the other ${account.provider} accounts on this proxy`;
  only.addEventListener("click", () => prefer(account, "only"));
  row.append(toggle);
  if (peers) row.append(only);
  if (proxied.setOn && proxied.setAt) row.append(el(document, "span", "usage-account-sub", `set on ${proxied.setOn} at ${clock(proxied.setAt)}`));
  return row;
}

export function planQuery(proxied, set, now) {
  return new URLSearchParams({ proxy: proxied.proxyId, account: proxied.id, set, now: String(Boolean(now)) }).toString();
}

export function planLines(plan) {
  if (plan.nothing) return [plan.nothing];
  const lines = plan.changes.map((change) => `${change.label}: preference ${change.before} → ${change.after}`);
  if (plan.set !== "only") lines.push("Other accounts are not changed.");
  if (plan.release.length) lines.push(`Releases the sessions on ${plan.release.map((account) => account.label).join(", ")}.`);
  for (const host of plan.hosts) {
    const running = host.sessions.map((session) => session.name);
    lines.push(`${host.host}: ${host.roots.join(", ")}${running.length ? ` · running: ${running.join(", ")}` : " · no running sessions"}`);
  }
  return lines.concat(plan.warnings || []);
}

async function answer(response) {
  const body = await response.json().catch(() => ({}));
  if (!response.ok) {
    const error = new Error(body.error?.message || `HTTP ${response.status}`);
    error.code = body.error?.code;
    throw error;
  }
  return body;
}

export function openPrefer({ document, fetch, account, set, notify, reload }) {
  const dialog = document.getElementById("prefer-dialog");
  const form = document.getElementById("prefer-form");
  const list = document.getElementById("prefer-plan");
  const nowRow = document.getElementById("prefer-now-row");
  const nowBox = document.getElementById("prefer-now");
  const error = document.getElementById("prefer-error");
  const confirm = document.getElementById("prefer-confirm");
  const proxied = account.proxyAccount;
  let plan = null;
  const fail = (message) => {
    error.textContent = message;
    error.hidden = false;
  };
  const load = async () => {
    plan = null;
    confirm.disabled = true;
    error.hidden = true;
    list.replaceChildren(el(document, "li", "usage-note", "Reading the proxy…"));
    try {
      plan = await answer(await fetch(`/api/v1/proxies/prefer?${planQuery(proxied, set, nowBox.checked)}`, { headers: { accept: "application/json" } }));
    } catch (failure) {
      list.replaceChildren();
      fail(failure.message);
      return;
    }
    list.replaceChildren(...planLines(plan).map((line) => el(document, "li", "", line)));
    confirm.textContent = plan.confirm || "Nothing to change";
    confirm.disabled = Boolean(plan.nothing);
  };
  document.getElementById("prefer-dialog-title").textContent = `${account.login} on the ${account.group.name} proxy`;
  nowBox.checked = false;
  nowRow.hidden = set !== "only";
  nowBox.onchange = load;
  document.getElementById("prefer-dialog-close").onclick = () => dialog.close();
  form.onsubmit = async (event) => {
    event.preventDefault();
    if (!plan || plan.nothing) return;
    confirm.disabled = true;
    try {
      const result = await answer(await fetch("/api/v1/proxies/prefer", { method: "POST", headers: { "content-type": "application/json", accept: "application/json" }, body: JSON.stringify({ proxy: proxied.proxyId, account: proxied.id, set, now: nowBox.checked, planId: plan.planId }) }));
      dialog.close();
      notify(result.message);
      reload();
    } catch (failure) {
      if (failure.code === "proxy_write_failed") reload();
      if (failure.code === "plan_stale" || failure.code === "proxy_write_failed") await load();
      else confirm.disabled = false;
      fail(failure.message);
    }
  };
  dialog.showModal();
  return load();
}
