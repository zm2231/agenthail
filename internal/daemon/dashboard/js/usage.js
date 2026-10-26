import { fit, wrapped } from "./usage-fit.js";

export const POLL_MS = 120_000;
const REDRAW_MS = 60_000;

const time = (value) => {
  const parsed = Date.parse(value || "");
  return Number.isNaN(parsed) ? null : parsed;
};

export function visible(meters, now) {
  return (meters || []).filter((meter) => {
    const until = time(meter.until);
    return until === null || now < until;
  });
}

export function percent(value) {
  return `${Math.round(value)}%`;
}

export function reset(ms) {
  const seconds = Math.max(Math.floor(ms / 1000), 0);
  const days = Math.floor(seconds / 86_400);
  const hours = Math.floor((seconds % 86_400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `resets ${hours}h ${minutes}m`;
  return `resets ${minutes}m`;
}

export function ago(ms) {
  const hours = Math.floor(ms / 3_600_000);
  return hours >= 24 ? `${Math.floor(hours / 24)}d` : `${Math.max(hours, 1)}h`;
}

export function read(ms) {
  const minutes = Math.floor(ms / 60_000);
  if (minutes < 1) return "read just now";
  if (minutes < 60) return `read ${minutes}m ago`;
  return `read ${ago(ms)} ago`;
}

export function detail(meter, now) {
  const asOf = time(meter.asOf);
  if (meter.aged && asOf !== null) return `as of ${ago(now - asOf)} ago`;
  const resets = time(meter.resetsAt);
  return resets === null ? null : reset(resets - now);
}

export function text(meter, now) {
  const extra = detail(meter, now);
  return percent(meter.percent) + (extra ? ` · ${extra}` : "");
}

export function spoken(meter, now) {
  const extra = detail(meter, now);
  return `${meter.label} usage ${Math.round(meter.percent)} percent` + (extra ? `, ${extra}` : "");
}

export function shortLabel(meter) {
  if (meter.kind === "session" || meter.kind === "5h") return "Session";
  if (meter.kind === "weekly" || meter.kind === "weekly_all") return "Week";
  if (meter.kind === "weekly_scoped") return "Week, model";
  return meter.label;
}

const critical = (meter) => meter.severity === "critical" || meter.percent >= 90;

function baseName(set) {
  if (!set.provider) return set.host;
  if (set.provider === "codex") return "Codex";
  if (set.account && set.account.includes("@")) return set.account.split("@")[0];
  return set.name || set.account || "Claude";
}

function login(set) {
  if (!set.provider) return set.host;
  if (set.provider === "codex") return "Codex";
  return set.account || set.name || "Claude";
}

export function accounts(sets, now) {
  const names = (sets || []).map(baseName);
  const hostsByName = new Map();
  (sets || []).forEach((set, index) => {
    const hosts = hostsByName.get(names[index]) || new Set();
    hosts.add(set.hostId);
    hostsByName.set(names[index], hosts);
  });
  return (sets || []).map((set, index) => {
    const meters = visible(set.meters, now);
    const shared = hostsByName.get(names[index]).size > 1 && set.provider;
    const name = login(set);
    const fullTitle = set.local === true || !set.provider ? name : `${name} on ${set.host}`;
    const parts = meters.map((meter) => spoken(meter, now)).concat(set.problem ? [set.problem] : []);
    const asOf = time(set.asOf);
    return {
      id: `${set.hostId}|${set.provider || ""}|${set.root || ""}`,
      title: shared ? `${set.host} · ${names[index]}` : names[index],
      login: name,
      fullTitle,
      host: set.host,
      hostId: set.hostId,
      provider: set.provider || null,
      rootName: set.name || null,
      root: set.root || null,
      meters,
      worst: meters.reduce((top, meter) => (!top || meter.percent > top.percent ? meter : top), null),
      critical: meters.some(critical),
      problem: set.problem || null,
      subtitle: [set.provider === "claude" && set.name !== name ? set.name : null, asOf === null ? null : read(now - asOf)].filter(Boolean).join(" · ") || null,
      spoken: `${fullTitle}: ${parts.length ? parts.join("; ") : "no usage reading"}`,
    };
  });
}

export function byHost(list) {
  const groups = new Map();
  for (const account of list) {
    if (!groups.has(account.hostId)) groups.set(account.hostId, { id: account.hostId, name: account.host, accounts: [] });
    groups.get(account.hostId).accounts.push(account);
  }
  return [...groups.values()];
}

export function owns(account, session) {
  if (session.archived || session.host?.id !== account.hostId) return false;
  if (account.provider === "claude") return session.surface === "claude" && session.claudeConfigRoot?.root === account.root;
  if (account.provider === "codex") return session.surface === "codex";
  return false;
}

const working = (session) => session.status === "busy";

export function activeSessions(account, sessions) {
  return (sessions || [])
    .filter((session) => owns(account, session) && (working(session) || session.current))
    .sort((a, b) => Number(working(b)) - Number(working(a)) || String(b.lastActive || "").localeCompare(String(a.lastActive || "")));
}

function node(document, tag, className, content) {
  const element = document.createElement(tag);
  if (className) element.className = className;
  if (content !== undefined) element.textContent = content;
  return element;
}

function bar(document, meter) {
  const element = document.createElement("meter");
  element.min = 0;
  element.max = 100;
  element.value = Math.min(Math.max(meter.percent, 0), 100);
  if (critical(meter)) element.className = "critical";
  return element;
}

export function render(document, element, list) {
  const chips = list.map((account) => {
    const chip = node(document, "span", ["usage-chip", account.critical ? "critical" : "", account.problem && !account.meters.length ? "problem" : ""].filter(Boolean).join(" "));
    chip.append(node(document, "span", "usage-label", account.title));
    if (account.meters.length) {
      const bars = node(document, "span", "usage-bars");
      bars.append(...account.meters.slice(0, 3).map((meter) => bar(document, meter)));
      chip.append(bars);
    }
    chip.append(node(document, "span", "usage-value", account.worst ? percent(account.worst.percent) : account.problem ? "!" : "–"));
    chip.title = account.spoken;
    chip.setAttribute("aria-label", account.spoken);
    return chip;
  });
  const more = node(document, "span", "usage-more");
  more.hidden = true;
  element.replaceChildren(...chips, more);
  element.hidden = chips.length === 0;
  element.setAttribute("aria-label", list.length ? `Usage of ${list.length} account${list.length === 1 ? "" : "s"}. ${list.map((a) => a.spoken).join(". ")}` : "Usage limits");
}

function accountCard(document, account, sessions, now, open) {
  const card = node(document, "section", "usage-account");
  const head = node(document, "div", "usage-account-head");
  head.append(node(document, "strong", "", account.login));
  if (account.subtitle) head.append(node(document, "span", "usage-account-sub", account.subtitle));
  card.append(head);
  for (const meter of account.meters) {
    const row = node(document, "div", critical(meter) ? "usage-row critical" : "usage-row");
    const resets = time(meter.resetsAt);
    row.append(node(document, "span", "usage-row-label", shortLabel(meter)), bar(document, meter), node(document, "span", "usage-value", percent(meter.percent)), node(document, "span", "usage-row-reset", resets === null ? "" : reset(resets - now)));
    row.setAttribute("aria-label", spoken(meter, now));
    card.append(row);
  }
  if (account.problem) card.append(node(document, "p", account.meters.length ? "usage-note" : "usage-note bad", account.problem));
  else if (!account.meters.length) card.append(node(document, "p", "usage-note", "No current reading"));
  if (sessions.length) {
    const list = node(document, "div", "usage-sessions");
    for (const session of sessions.slice(0, 5)) {
      const button = node(document, "button", working(session) ? "usage-session working" : "usage-session");
      button.type = "button";
      button.append(node(document, "i"), node(document, "span", "", session.alias ? `@${session.alias}` : session.name || session.id));
      button.addEventListener("click", () => open(session.id));
      list.append(button);
    }
    if (sessions.length > 5) list.append(node(document, "span", "usage-note", `and ${sessions.length - 5} more`));
    card.append(list);
  }
  return card;
}

export function renderPanel(document, panel, list, sessions, now, open) {
  const groups = byHost(list);
  const children = [node(document, "h2", "usage-panel-title", "Accounts")];
  for (const group of groups) {
    if (groups.length > 1) children.push(node(document, "h3", "usage-host", group.name));
    for (const account of group.accounts) children.push(accountCard(document, account, activeSessions(account, sessions), now, open));
  }
  panel.replaceChildren(...children);
}

export function start({ document, fetch, setInterval, now = Date.now, sessions = () => [], open = () => {} }) {
  const element = document.getElementById("usage-line");
  const panel = document.getElementById("usage-panel");
  if (!element) return null;
  let sets = [];
  const drawPanel = () => {
    if (panel && !panel.hidden) renderPanel(document, panel, accounts(sets, now()), sessions(), now(), (id) => { toggle(false); open(id); });
  };
  const toggle = (show) => {
    if (!panel) return;
    panel.hidden = !show;
    element.setAttribute("aria-expanded", String(show));
    drawPanel();
  };
  const draw = () => {
    render(document, element, accounts(sets, now()));
    fit(element, wrapped);
    drawPanel();
  };
  element.addEventListener?.("click", () => toggle(Boolean(panel?.hidden)));
  document.addEventListener?.("keydown", (event) => { if (event.key === "Escape" && panel && !panel.hidden) toggle(false); });
  document.addEventListener?.("click", (event) => {
    if (panel && !panel.hidden && !panel.contains(event.target) && !element.contains(event.target)) toggle(false);
  });
  if (typeof ResizeObserver !== "undefined") new ResizeObserver(() => fit(element, wrapped)).observe(element);
  const load = async () => {
    try {
      const response = await fetch("/api/v1/usage", { headers: { accept: "application/json" } });
      if (response.status === 404) sets = [];
      else if (response.ok) sets = (await response.json()).sets || [];
    } catch {}
    draw();
  };
  const ready = load();
  setInterval(load, POLL_MS);
  setInterval(draw, REDRAW_MS);
  return { ready, load, toggle, current: () => sets };
}
