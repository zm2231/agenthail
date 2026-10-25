import { escape } from "./format.js";
import { markdown } from "./markdown.js";

export function handoffMessage(value) {
  let text = String(value || "");
  let label = "Conversation input";
  let match;
  while ((match = text.match(/^\[from\s+([^\]]+)\]\s*/i))) {
    label = `From ${match[1]}`;
    text = text.slice(match[0].length);
  }
  return { label, text };
}

export function exchangePrompt(exchange) {
  const message = handoffMessage(exchange.user);
  if (exchange.origin === "peer") return { ...message, label: exchange.sender ? `From ${exchange.sender}` : "From an agent" };
  if (exchange.origin === "task-notification") return { ...message, label: "Task notification" };
  if (exchange.origin === "auto-continuation") return { ...message, label: "Continued" };
  if (exchange.origin === "agenthail-notice") return { ...message, label: "Agenthail" };
  if (exchange.origin) return { ...message, label: exchange.origin };
  return message;
}

export function renderMessage(text, kind, label, turnKey, files = [], footer = "") {
  const safeText = String(text || "");
  const editable = kind === "user" && label === "Conversation input";
  const edit = editable ? `<button class="turn-action" type="button" data-edit-message="${escape(safeText)}">Edit</button>` : "";
  const content = safeText ? `<div class="turn-content">${markdown(safeText)}</div>` : "";
  const actions = safeText ? `<span class="turn-actions"><button class="turn-action" type="button" data-copy-text="${escape(safeText)}">Copy</button>${edit}</span>` : "";
  const status = footer ? `<span class="turn-status">${escape(footer)}</span>` : "";
  const footerLine = status || actions ? `<footer class="turn-footer">${status}${actions}</footer>` : "";
  return `<article class="turn ${kind}" data-turn-key="${escape(turnKey)}"><header class="turn-header"><span class="turn-label">${escape(label)}</span></header>${content}${renderAttachedFiles(files)}${footerLine}</article>`;
}

export function fileSize(bytes) {
  if (!bytes) return "";
  const units = ["bytes", "KB", "MB", "GB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1000 && unit < units.length - 1) {
    value /= 1000;
    unit += 1;
  }
  return unit === 0 ? `${value} ${units[0]}` : `${value.toFixed(value < 10 ? 1 : 0)} ${units[unit]}`;
}

export function renderAttachedFiles(files) {
  if (!files?.length) return "";
  return `<div class="attached-files">${files.map(file => {
    const size = fileSize(file.bytes);
    return `<button class="attached-file" type="button" data-copy-text="${escape(file.path || "")}" data-copy-done="Path copied." title="${escape(file.path || "")}"><span class="attached-file-name">${escape(file.name || "File")}</span>${size ? `<span class="attached-file-size">${escape(size)}</span>` : ""}</button>`;
  }).join("")}</div>`;
}
