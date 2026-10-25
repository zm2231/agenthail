import { $ } from "./dom.js";

export function friendlyError(error) {
  return /failed to fetch|networkerror/i.test(String(error))
    ? "Agenthail is not connected. Restart it from the menu bar, then refresh this page."
    : String(error);
}

export function toast(message) {
  const node = $("#toast");
  node.textContent = message;
  node.hidden = false;
  clearTimeout(toast.timer);
  toast.timer = setTimeout(() => {
    node.hidden = true;
  }, 5000);
}
