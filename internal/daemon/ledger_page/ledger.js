import { shownRows, shortList, withoutRefs } from "./ledger-text.js";

const project = new URLSearchParams(location.search).get("project") || "";
const root = document.getElementById("work");
const expanded = new Set();
const opened = new Set();
let doneOpen = false;
let etag = "";
let view = null;
let titles = new Map();

const priorityWords = { P0: "Urgent", P1: "High priority" };

function plain(text) {
  if (!text) return text;
  return withoutRefs(text.replace(/\b[A-Za-z0-9][A-Za-z0-9-]*-W\d+\b/g, (id) => (titles.has(id) ? `“${titles.get(id)}”` : id)));
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

function chip(className, text) {
  return text ? node("span", `chip ${className}`, text) : null;
}

function rowChips(row) {
  return [
    chip("holder", row.holder),
    priorityWords[row.priority] && chip(`priority ${row.priority.toLowerCase()}`, priorityWords[row.priority]),
    row.type && row.type !== "task" && chip("type", row.type),
    chip("since", ago(row.since)),
  ];
}

function line(className, text) {
  const value = plain(text);
  return value ? node("div", className, value) : null;
}

function itemLines(key, title, chips, details) {
  const item = node("li");
  const open = opened.has(key);
  const shown = details.filter(Boolean);
  const head = node("button", "row");
  head.type = "button";
  head.setAttribute("aria-expanded", String(open));
  head.append(node("span", "title", plain(title)));
  const meta = node("span", "meta");
  for (const value of chips) if (value) meta.append(value);
  if (meta.childElementCount) head.append(meta);
  head.addEventListener("click", () => {
    if (opened.has(key)) opened.delete(key);
    else opened.add(key);
    render();
  });
  item.append(head);
  if (open) {
    const body = node("div", "details");
    body.append(...(shown.length ? shown : [node("div", "note", "Nothing more recorded.")]));
    item.append(body);
  }
  return item;
}

function purpose(row) {
  if (!row.for) return null;
  const value = plain(row.for);
  if (!value) return null;
  const element = node("div", "for", "For: ");
  element.append(node("q", "", value));
  return element;
}

function workRow(scope, row, extra = []) {
  return itemLines(`${scope}:${row.id}`, row.title, rowChips(row), [line("what", row.what), purpose(row), ...extra]);
}

function rowFor(state, row) {
  if (state === "blocked") return workRow(state, row, [row.note && line("note", `Blocked: ${row.note}`)]);
  if (state === "parked" && row.parked) {
    const why = [parkedAs[row.parked.kind] || "Set aside", row.parked.why].filter(Boolean).join(": ");
    const revisit = row.parked.revisit ? ` (look again ${day(row.parked.revisit)})` : "";
    return workRow(state, row, [line("note", why + revisit)]);
  }
  return workRow(state, row);
}

function list(key, rows, build) {
  const items = node("ol");
  const all = expanded.has(key);
  for (const row of shownRows(rows, all)) items.append(build(row));
  const out = [items];
  if (rows.length > shortList) {
    const more = node("button", "more", all ? "Show fewer" : `Show all ${rows.length}`);
    more.type = "button";
    more.addEventListener("click", () => {
      if (all) expanded.delete(key);
      else expanded.add(key);
      render();
    });
    out.push(more);
  }
  return out;
}

function heading(title, count) {
  const element = node("h2", "", title);
  element.append(node("span", "count", String(count)));
  return element;
}

function section(key, title, rows, build) {
  const block = node("section", key.replace("_", "-"));
  block.append(heading(title, rows.length), ...list(key, rows, build));
  return block;
}

function waitingRow(wait) {
  const asked = [wait.from && `Asked by ${wait.from}`, wait.deadline && `answer by ${day(wait.deadline)}`].filter(Boolean).join(", ");
  const details = [wait.recommendation && line("recommend", `Suggested: ${wait.recommendation}`), asked && line("for", asked)];
  if (wait.work) return itemLines(`waiting:${wait.id}`, wait.work.title, rowChips({ ...wait.work, holder: "" }), [line("note", wait.question), ...details]);
  return itemLines(`waiting:${wait.id}`, wait.question, [], details);
}

function doneRow(row) {
  const done = [`Done ${ago(row.since)}`, row.reviewer && `checked by ${row.reviewer}`].filter(Boolean).join(", ");
  return itemLines(`done:${row.id}`, row.title, [chip("since", ago(row.since))], [line("what", row.what), purpose(row), row.note && line("note", row.note), line("for", done)]);
}

function readyRow(row) {
  const why = [parkedAs[row.parked.kind] || "Set aside", row.parked.why].filter(Boolean).join(": ");
  return itemLines(`ready:${row.id}`, row.title, [chip("ready", "Ready"), ...rowChips(row)], [line("ready-why", `Ready: ${row.parked.ready}`), line("what", row.what), purpose(row), line("note", why)]);
}

function doneSection(rows) {
  const block = node("section", "done");
  const toggle = node("button", "fold");
  toggle.type = "button";
  toggle.setAttribute("aria-expanded", String(doneOpen));
  toggle.append(heading("Recently done", rows.length));
  toggle.addEventListener("click", () => {
    doneOpen = !doneOpen;
    render();
  });
  block.append(toggle);
  if (doneOpen) block.append(...list("done", rows, doneRow));
  return block;
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
  if (view.done.length) parts.push(doneSection(view.done));
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
