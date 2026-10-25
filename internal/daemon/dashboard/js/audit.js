import { escape, timeAgo } from "./format.js";
import { app } from "./state.js";
import { $ } from "./dom.js";
import { markdown } from "./markdown.js";
import { responseError } from "./http.js";
import { friendlyError, toast } from "./feedback.js";

export function auditKindLabel(kind) {
  const words = String(kind || "event").replace(/[_-]+/g, " ").trim().toLowerCase();
  return words.charAt(0).toUpperCase() + words.slice(1);
}

export function auditHeading(entry) {
  const operation = entry.operation && entry.operation !== "message" ? auditKindLabel(entry.operation) : "";
  return operation ? `${auditKindLabel(entry.kind)} · ${operation}` : auditKindLabel(entry.kind);
}

export function auditRoute(entry) {
  const target = entry.target || entry.sessionId || "Agenthail";
  return entry.source && entry.source !== target ? `${escape(entry.source)} <span class="route-arrow">→</span> ${escape(target)}` : escape(target);
}

export function renderAudit() {
  const audit = app.audit;
  const kindSelect = $("#audit-kind-filter");
  kindSelect.innerHTML = ['<option value="all">All events</option>', ...audit.kinds.map((kind) => `<option value="${escape(kind)}">${escape(auditKindLabel(kind))}</option>`)].join("");
  kindSelect.value = audit.kinds.includes(audit.kind) ? audit.kind : "all";
  audit.kind = kindSelect.value;
  $("#history-list").innerHTML = audit.items.map((entry) => {
    const route = auditRoute(entry);
    const content = entry.error || entry.message || entry.result || "No message content recorded";
    return `<article class="audit-event"><div class="audit-marker ${entry.error ? "failed" : ""}"></div><div class="audit-event-body"><div class="audit-event-heading"><strong>${escape(auditHeading(entry))}</strong><time>${timeAgo(entry.createdAt)}</time></div><div class="audit-route">${route}</div><details><summary>View details</summary><div class="audit-detail${entry.error ? " operation-error" : ""}">${markdown(content)}</div></details></div></article>`;
  }).join("") || `<div class="empty-card">${audit.loading ? "Loading activity." : audit.loaded ? "No activity matches these filters." : "Open Audit to load activity."}</div>`;
  const more = $("#history-more");
  more.hidden = !audit.loaded || !audit.hasMore;
  more.disabled = audit.loading;
  more.textContent = audit.loading ? "Loading activity" : "Load older activity";
}

export async function loadAudit(reset = false, silent = false) {
  if (app.audit.loading) return;
  const previous = silent ? { items: app.audit.items, nextBefore: app.audit.nextBefore, hasMore: app.audit.hasMore } : null;
  if (reset) app.audit = { ...app.audit, items: silent ? app.audit.items : [], nextBefore: 0, hasMore: true, loaded: silent && app.audit.loaded };
  if (!app.audit.hasMore) return;
  app.audit.loading = true;
  renderAudit();
  try {
    const params = new URLSearchParams({ limit: "25" });
    if (app.audit.nextBefore) params.set("before", app.audit.nextBefore);
    if (app.audit.kind !== "all") params.set("kind", app.audit.kind);
    if (app.audit.query) params.set("q", app.audit.query);
    const response = await fetch(`/api/history?${params}`);
    if (!response.ok) throw await responseError(response);
    const page = await response.json();
    const previousIDs = previous ? new Set(previous.items.map((entry) => entry.id)) : null;
    const overlapsPrevious = previousIDs ? page.items.some((entry) => previousIDs.has(entry.id)) : false;
    if (reset) {
      if (previous) {
        const fresh = new Set(page.items.map((entry) => entry.id));
        app.audit.items = [...page.items, ...previous.items.filter((entry) => !fresh.has(entry.id))];
      } else {
        app.audit.items = page.items;
      }
    } else {
      const known = new Set(app.audit.items.map((entry) => entry.id));
      app.audit.items.push(...page.items.filter((entry) => !known.has(entry.id)));
    }
    app.audit.nextBefore = previous && overlapsPrevious ? previous.nextBefore : page.nextBefore || 0;
    app.audit.hasMore = previous && overlapsPrevious ? previous.hasMore : page.hasMore === true;
    app.audit.kinds = page.kinds || [];
    app.audit.loaded = true;
  } catch (error) {
    toast(friendlyError(error));
  } finally {
    app.audit.loading = false;
    renderAudit();
  }
}

export function bindAuditEvents() {
  $("#audit-search").addEventListener("input", (event) => {
    app.audit.query = event.target.value;
    clearTimeout(app.audit.searchTimer);
    app.audit.searchTimer = setTimeout(() => loadAudit(true), 250);
  });
  $("#audit-kind-filter").addEventListener("change", (event) => {
    app.audit.kind = event.target.value;
    loadAudit(true);
  });
  $("#history-more").addEventListener("click", () => loadAudit());
}
