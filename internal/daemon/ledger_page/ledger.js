const project = new URLSearchParams(location.search).get("project") || "";
const root = document.getElementById("work");
const shortList = 25;
const expanded = new Set();
let etag = "";
let view = null;
let titles = new Map();

const priorityWords = { P0: "Urgent", P1: "High priority" };

function plain(text) {
  if (!text) return text;
  return text.replace(/\b[A-Za-z0-9][A-Za-z0-9-]*-W\d+\b/g, (id) => (titles.has(id) ? `“${titles.get(id)}”` : id));
}

function learnTitles() {
  titles = new Map();
  const rows = [...Object.values(view.work).flat(), ...(view.ready || []), ...view.done, ...view.waiting.map((wait) => wait.work).filter(Boolean)];
  for (const row of rows) if (row.id && row.title) titles.set(row.id, row.title);
}
let reading = false;
let again = false;

const sections = [
  ["in_progress", "In progress"],
  ["in_review", "In review"],
  ["blocked", "Blocked"],
  ["open", "Up next"],
  ["parked", "Set aside"],
];

const parkedAs = {
  usage: "Waiting for usage to come back",
  idea: "An idea to shape later",
  later: "Set aside for later",
};

function node(tag, className, text) {
  const element = document.createElement(tag);
  if (className) element.className = className;
  if (text !== undefined) element.textContent = text;
  return element;
}

function ago(value) {
  const then = Date.parse(value || "");
  if (!then) return "";
  const minutes = Math.max(0, Math.round((Date.now() - then) / 60000));
  if (minutes < 2) return "just now";
  if (minutes < 60) return `${minutes} min ago`;
  if (minutes < 1440) return `${Math.round(minutes / 60)} h ago`;
  return `${Math.round(minutes / 1440)} d ago`;
}

function day(value) {
  const at = new Date(value || "");
  return Number.isNaN(at.getTime()) ? "" : at.toLocaleDateString([], { month: "short", day: "numeric" });
}

function itemLines(row, extra = []) {
  const item = node("li");
  item.dataset.id = row.id;
  item.title = row.id;
  item.append(node("div", "title", plain(row.title)));
  if (row.what) item.append(node("div", "what", plain(row.what)));
  if (row.for) {
    const purpose = node("div", "for", "For: ");
    purpose.append(node("q", "", plain(row.for)));
    item.append(purpose);
  }
  for (const line of extra) if (line) item.append(line);
  const meta = node("div", "meta");
  if (row.holder) meta.append(node("span", "holder", row.holder));
  if (priorityWords[row.priority]) meta.append(node("span", `priority ${row.priority.toLowerCase()}`, priorityWords[row.priority]));
  if (row.type && row.type !== "task") meta.append(node("span", "type", row.type));
  if (row.since) meta.append(node("span", "since", ago(row.since)));
  if (meta.childElementCount) item.append(meta);
  return item;
}

function rowFor(state, row) {
  if (state === "blocked") return itemLines(row, [row.note && node("div", "note", `Blocked: ${plain(row.note)}`)]);
  if (state === "parked" && row.parked) {
    const why = [parkedAs[row.parked.kind] || "Set aside", plain(row.parked.why)].filter(Boolean).join(": ");
    const revisit = row.parked.revisit ? ` (look again ${day(row.parked.revisit)})` : "";
    return itemLines(row, [node("div", "note", why + revisit)]);
  }
  return itemLines(row);
}

function list(key, rows, build) {
  const items = node("ol");
  const all = expanded.has(key);
  for (const row of all ? rows : rows.slice(0, shortList)) items.append(build(row));
  const out = [items];
  if (!all && rows.length > shortList) {
    const more = node("button", "more", `Show all ${rows.length}`);
    more.type = "button";
    more.addEventListener("click", () => {
      expanded.add(key);
      render();
    });
    out.push(more);
  }
  return out;
}

function section(key, title, rows, build) {
  const block = node("section", key.replace("_", "-"));
  const heading = node("h2", "", title);
  heading.append(node("span", "count", String(rows.length)));
  block.append(heading, ...list(key, rows, build));
  return block;
}

function waitingRow(wait) {
  const lines = [];
  if (wait.recommendation) lines.push(node("div", "recommend", `Suggested: ${wait.recommendation}`));
  const asked = [wait.from && `Asked by ${wait.from}`, wait.deadline && `answer by ${day(wait.deadline)}`].filter(Boolean).join(", ");
  if (asked) lines.push(node("div", "for", asked));
  if (wait.work) {
    return itemLines({ ...wait.work, holder: "" }, [node("div", "note", plain(wait.question)), ...lines]);
  }
  const item = node("li");
  item.dataset.id = wait.id;
  item.append(node("div", "title", plain(wait.question)), ...lines);
  return item;
}

function doneRow(row) {
  return itemLines({ ...row, since: "" }, [
    row.note && node("div", "note", plain(row.note)),
    node("div", "for", [`Done ${ago(row.since)}`, row.reviewer && `checked by ${row.reviewer}`].filter(Boolean).join(", ")),
  ]);
}

function readyRow(row) {
  const why = [parkedAs[row.parked.kind] || "Set aside", plain(row.parked.why)].filter(Boolean).join(": ");
  return itemLines(row, [node("div", "ready-why", `Ready: ${plain(row.parked.ready)}`), node("div", "note", why)]);
}

function render() {
  if (!view) return;
  learnTitles();
  const parts = [];
  if (view.ready?.length) parts.push(section("ready", "Ready to pick up", view.ready, readyRow));
  if (view.waiting.length) {
    const block = section("waiting", "Waiting on you", view.waiting, waitingRow);
    block.append(node("p", "note", "Answer these in Needs you."));
    parts.push(block);
  }
  for (const [state, title] of sections) {
    const rows = view.work[state] || [];
    if (rows.length) parts.push(section(state, title, rows, (row) => rowFor(state, row)));
  }
  if (view.done.length) parts.push(section("done", "Recently done", view.done, doneRow));
  if (!parts.length) parts.push(node("p", "note", "No work recorded for this project yet. Items appear here as sessions add them."));
  root.replaceChildren(...parts);
}

async function fetchView() {
  const response = await fetch(`ledger?project=${encodeURIComponent(project)}`, { credentials: "same-origin", cache: "no-store", headers: etag ? { "If-None-Match": etag } : {} });
  if (response.status === 304) return;
  if (!response.ok) {
    const body = await response.json().catch(() => ({}));
    throw new Error(body.error?.message || `The work list could not be read (${response.status}).`);
  }
  etag = response.headers.get("ETag") || "";
  view = await response.json();
  render();
}

async function read() {
  if (reading) {
    again = true;
    return;
  }
  reading = true;
  do {
    again = false;
    try {
      await fetchView();
    } catch (error) {
      if (!view) root.replaceChildren(node("p", "error", error.message));
    }
  } while (again);
  reading = false;
}

if (!project) {
  root.replaceChildren(node("p", "error", "This page shows one project's work; open it from a project."));
} else {
  read();
  const events = new EventSource(`events?project=${encodeURIComponent(project)}`, { withCredentials: true });
  events.addEventListener("ledger.changed", read);
  events.addEventListener("open", read);
}
