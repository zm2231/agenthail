import { clockTime, escape, labels, turnFooter } from "./format.js";
import { markdown } from "./markdown.js";
import { handoffMessage, renderMessage } from "./messages.js";
import { toolKinds } from "./tool-view.js";
import { renderToolRun } from "./tool-runs.js";

export function renderImageAttachment(item, session) {
  const attachment = item.attachment;
  const fallback = escape(item.text || "Image attachment");
  if (!attachment?.id || !/^image\/(png|jpeg|gif|webp|avif|heic|heif)$/.test(attachment.mediaType || ""))
    return `<div class="turn"><p>${fallback}</p></div>`;
  const url = `/api/session-attachment?sessionId=${encodeURIComponent(session.id)}&id=${encodeURIComponent(attachment.id)}`;
  return `<figure class="turn transcript-image"><img src="${escape(url)}" loading="lazy" decoding="async" alt="${fallback}" referrerpolicy="no-referrer"><figcaption>${fallback}</figcaption></figure>`;
}

export function timelineKindClass(kind) {
  return String(kind || "unknown").replace(/[^a-zA-Z0-9_-]/g, "-");
}

export function timelineItemAttributes(item) {
  const attributes = [`data-timeline-item="${escape(item.id || "")}"`];
  if (item.callId) attributes.push(`data-call-id="${escape(item.callId)}"`);
  if (item.status) attributes.push(`data-status="${escape(item.status)}"`);
  return attributes.join(" ");
}

export function renderTimelineMetadata(item) {
  return item.truncated ? '<div class="timeline-meta">Shortened by the host</div>' : "";
}

export function isInstructions(item) {
  return item.kind === "instructions" || (["message", "text"].includes(item.kind) && ["developer", "system"].includes(item.role));
}

export function renderTimelineItem(item, session, turnEnd = null) {
  const kind = String(item.kind || "unknown");
  const kindClass = timelineKindClass(kind);
  const attributes = timelineItemAttributes(item);
  const metadata = renderTimelineMetadata(item);
  if (kind === "attachment") {
    return `<section class="timeline-item timeline-${kindClass}" ${attributes}>${renderImageAttachment(item, session)}${metadata}</section>`;
  }
  if (isInstructions(item)) {
    const body = item.text ? markdown(item.text) : '<p class="timeline-empty">No recorded content.</p>';
    return `<details class="timeline-item timeline-instructions timeline-collapsible" ${attributes}><summary>Instructions</summary>${metadata}<div class="turn-content">${body}</div></details>`;
  }
  if (kind === "message" && item.role === "peer") {
    return `<section class="timeline-item timeline-${kindClass}" ${attributes}>${renderMessage(item.text, "peer", `From ${item.sender || "another agent"}`, `${session.id}:${item.id}`, [], clockTime(item.timestamp))}${metadata}</section>`;
  }
  if (["text", "message", "assistant"].includes(kind)) {
    const user = item.role === "user";
    const content = user ? handoffMessage(item.text) : { text: item.text, label: labels[session.surface] || session.surface };
    const kindName = !user ? "agent" : content.label.startsWith("From ") ? "peer" : "user";
    const footer = user ? clockTime(item.timestamp) : turnEnd ? turnFooter(turnEnd.text, turnEnd.timestamp || item.timestamp) : "";
    return `<section class="timeline-item timeline-${kindClass}" ${attributes}>${renderMessage(content.text, kindName, content.label, `${session.id}:${item.id}`, user ? item.files : [], footer)}${metadata}</section>`;
  }
  if (kind === "event") {
    const text = [item.title, item.status].filter(Boolean).join(" · ") + (item.text ? `: ${item.text}` : "");
    return `<p class="timeline-item timeline-divider" ${attributes}><span>${escape(text)}</span>${metadata}</p>`;
  }
  if (kind === "done") return "";
  if (kind === "notice") {
    const resets = Date.parse(item.resetsAt || "");
    const until = resets ? ` Resets ${new Date(resets).toLocaleString([], { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" })}.` : "";
    return `<div class="timeline-item timeline-notice" role="status" ${attributes}><strong>${escape(item.title || "Notice")}</strong> ${escape(item.text || "")}${escape(until)}${metadata}</div>`;
  }
  if (toolKinds.includes(kind)) return renderToolRun([item], session);
  const title = item.title || "Unknown timeline item";
  const body = item.text ? markdown(item.text) : '<p class="timeline-empty">No recorded content.</p>';
  return `<article class="turn timeline-item timeline-${kindClass} timeline-unknown" ${attributes}><header class="turn-header"><span class="turn-label">${escape(title)}</span><span class="timeline-kind">${escape(kind)}</span></header>${metadata}<div class="turn-content">${body}</div></article>`;
}
