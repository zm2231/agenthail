import { escape } from "./format.js";

export function queueStatusPill(status) {
  return `<span class="status-pill ${escape(status)}">${escape(status)}</span>`;
}
