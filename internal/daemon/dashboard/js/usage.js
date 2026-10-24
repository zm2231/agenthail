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

export function inUse(set) {
  return set.local === true && (set.primary === true || set.provider === "codex");
}

const providerName = (set) => (set.provider === "codex" ? "Codex" : "Claude");

export function title(set) {
  const name = set.account ? set.account.split("@")[0] : providerName(set);
  return set.local === true ? name : `${name} · ${set.host}`;
}

export function fullTitle(set) {
  const name = set.account || providerName(set);
  return set.local === true ? name : `${name} on ${set.host}`;
}

export function usageStrip(sets, now) {
  const strip = { meters: [], others: [], problem: null };
  const problems = [];
  for (const set of sets || []) {
    const shown = visible(set.meters, now);
    if (inUse(set)) {
      strip.meters.push(...shown);
      if (set.problem) problems.push(set.problem);
      continue;
    }
    if (!shown.length) continue;
    strip.others.push({
      id: `${set.hostId}|${set.provider || ""}|${set.root || ""}`,
      title: title(set),
      percent: Math.max(...shown.map((meter) => meter.percent)),
      critical: shown.some((meter) => meter.severity === "critical"),
      spoken: `${fullTitle(set)}: ${shown.map((meter) => spoken(meter, now)).join("; ")}`,
    });
  }
  if (!strip.meters.length) strip.problem = problems[0] || null;
  return strip;
}

function span(document, className, content) {
  const element = document.createElement("span");
  element.className = className;
  if (content !== undefined) element.textContent = content;
  return element;
}

export function render(document, element, strip, now) {
  const children = [];
  if (strip.problem) {
    const problem = span(document, "usage-problem", strip.problem);
    problem.title = strip.problem;
    children.push(problem);
  }
  for (const meter of strip.meters) {
    const critical = meter.severity === "critical";
    const item = span(document, critical ? "usage-meter critical" : "usage-meter");
    const bar = document.createElement("meter");
    bar.min = 0;
    bar.max = 100;
    bar.value = Math.min(Math.max(meter.percent, 0), 100);
    const extra = detail(meter, now);
    item.append(span(document, "usage-label", meter.label), bar, span(document, "usage-value", percent(meter.percent)));
    if (extra) item.append(span(document, "usage-detail", `· ${extra}`));
    item.title = spoken(meter, now);
    item.setAttribute("aria-label", item.title);
    children.push(item);
  }
  for (const other of strip.others) {
    const item = span(document, other.critical ? "usage-other critical" : "usage-other");
    item.append(span(document, "usage-label", other.title), span(document, "usage-value", percent(other.percent)));
    item.title = other.spoken;
    item.setAttribute("aria-label", other.spoken);
    children.push(item);
  }
  element.replaceChildren(...children);
  element.hidden = children.length === 0;
}

export function start({ document, fetch, setInterval, now = Date.now }) {
  const element = document.getElementById("usage-line");
  if (!element) return null;
  let sets = [];
  const draw = () => render(document, element, usageStrip(sets, now()), now());
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
  return { ready, load, current: () => sets };
}

if (typeof window !== "undefined" && window.document) {
  start({ document: window.document, fetch: window.fetch.bind(window), setInterval: window.setInterval.bind(window) });
}
