import { clockTime, escape, labels, turnFooter } from "./format.js";
import { markdown } from "./markdown.js";
import { fileSize, renderMessage, userLabel } from "./messages.js";
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

const sentFileLimit = 50 * 1024 * 1024;

export function renderSentFile(item, session) {
  const file = item.files?.[0] || {};
  const attachment = item.attachment;
  const name = attachment?.name || file.name || item.title || "File";
  const size = fileSize(attachment?.bytes || file.bytes);
  const caption = item.text ? `<p class="sent-file-caption">${escape(item.text)}</p>` : "";
  const sizeTag = size ? `<span class="attached-file-size">${escape(size)}</span>` : "";
  if (!attachment?.id) {
    const why = file.bytes > sentFileLimit ? "Over 50 MiB, open it on its computer" : "No longer available";
    return `<div class="turn sent-file">${caption}<div class="attached-files"><button class="attached-file" type="button" data-copy-text="${escape(file.path || "")}" data-copy-done="Path copied." title="${escape(file.path || "")}"><span class="attached-file-name">${escape(name)}</span>${sizeTag}<span class="attached-file-size">${why}</span></button></div></div>`;
  }
  const url = `/api/session-attachment?sessionId=${encodeURIComponent(session.id)}&id=${encodeURIComponent(attachment.id)}`;
  const open = attachment.mediaType === "application/pdf" ? `<a class="attached-file" href="${escape(url)}" target="_blank" rel="noopener noreferrer">Open</a>` : "";
  return `<div class="turn sent-file">${caption}<div class="attached-files"><a class="attached-file" href="${escape(url)}" download="${escape(name)}" rel="noreferrer"><span class="attached-file-name">${escape(name)}</span>${sizeTag}</a>${open}</div></div>`;
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

export function renderTimelineItem(item, session, turnEnd = null, after = "") {
  const kind = String(item.kind || "unknown");
  const kindClass = timelineKindClass(kind);
  const attributes = timelineItemAttributes(item);
  const metadata = renderTimelineMetadata(item);
  if (kind === "attachment") {
    return `<section class="timeline-item timeline-${kindClass}" ${attributes}>${renderImageAttachment(item, session)}${metadata}</section>`;
  }
  if (kind === "file") {
    return `<section class="timeline-item timeline-${kindClass}" ${attributes}>${renderSentFile(item, session)}${metadata}</section>`;
  }
  if (isInstructions(item)) {
    const body = item.text ? markdown(item.text) : '<p class="timeline-empty">No recorded content.</p>';
    return `<details class="timeline-item timeline-instructions timeline-collapsible" ${attributes}><summary>Instructions</summary>${metadata}<div class="turn-content">${body}</div></details>`;
  }
  if (["text", "message", "assistant"].includes(kind)) {
    const user = item.role === "user";
    const label = user ? userLabel : labels[session.surface] || session.surface;
    const footer = user ? clockTime(item.timestamp) : turnEnd ? turnFooter(turnEnd.text, turnEnd.timestamp || item.timestamp) : "";
    return `<section class="timeline-item timeline-${kindClass}" ${attributes}>${renderMessage(item.text, user ? "user" : "agent", label, `${session.id}:${item.id}`, user ? item.files : [], footer, after)}${metadata}</section>`;
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
