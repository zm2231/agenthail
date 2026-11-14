import { $ } from "./dom.js";

const rereadAfter = 10_000;
let page = null;
let readAt = 0;
let reading = null;

export function workFrameSrc(project, ledgerPage, where = location) {
  if (!project || !ledgerPage || !(ledgerPage.port > 0) || ledgerPage.error) return "";
  if (where.protocol === "https:" && !ledgerPage.published) return "";
  return `${where.protocol}//${where.hostname}:${ledgerPage.port}${ledgerPage.entry || "/"}?project=${encodeURIComponent(project.id)}`;
}

function readPage(then) {
  if (reading || Date.now() - readAt < rereadAfter) return;
  readAt = Date.now();
  reading = fetch("/api/v1/extensions", { credentials: "same-origin" })
    .then((response) => (response.ok ? response.json() : null))
    .then((view) => {
      page = view?.builtIn?.ledger || null;
    })
    .catch(() => {})
    .finally(() => {
      reading = null;
      then?.();
    });
}

function note(text) {
  const line = document.createElement("p");
  line.className = "project-empty";
  line.textContent = text;
  return line;
}

export function syncProjectWork(project, again) {
  const holder = $("#project-work");
  if (!holder) return;
  holder.hidden = !project;
  if (!project) {
    holder.dataset.src = "";
    holder.replaceChildren();
    return;
  }
  const src = workFrameSrc(project, page);
  if (!src) readPage(again);
  const shown = src || (page ? "unreachable" : "reading");
  if (holder.dataset.src === shown && holder.childElementCount) return;
  holder.dataset.src = shown;
  const header = document.createElement("header");
  const title = document.createElement("h2");
  title.textContent = "Work";
  header.append(title);
  if (!src) {
    holder.replaceChildren(header, note(page ? "This project's work list isn't reachable from this address." : "Reading this project's work."));
    return;
  }
  const frame = document.createElement("iframe");
  frame.className = "project-work-frame";
  frame.title = "Work";
  frame.src = src;
  holder.replaceChildren(header, frame);
}
