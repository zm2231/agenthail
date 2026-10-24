export const PANE_KINDS = ["chrome", "sidebar", "content", "float"];

function pinned(element, view) {
  for (let node = element; node && node.nodeType === 1; node = node.parentElement) {
    const position = view.getComputedStyle(node).position;
    if (position === "fixed" || position === "sticky") return true;
  }
  return false;
}

export function paneAreas(document, view) {
  const width = view.innerWidth;
  const height = view.innerHeight;
  const areas = Object.fromEntries(PANE_KINDS.map((kind) => [kind, []]));
  for (const element of document.querySelectorAll("[data-pane]")) {
    const kind = element.dataset.pane;
    const rect = element.getBoundingClientRect();
    if (!areas[kind] || rect.width <= 0 || rect.height <= 0) continue;
    const left = Math.max(Math.floor(rect.left), 0);
    const right = Math.min(Math.ceil(rect.right), width);
    if (right <= left) continue;
    if (!pinned(element, view)) {
      areas[kind].push([left, 0, right - left, height]);
      continue;
    }
    const top = Math.max(Math.floor(rect.top), 0);
    const bottom = Math.min(Math.ceil(rect.bottom), height);
    if (bottom > top) areas[kind].push([left, top, right - left, bottom - top]);
  }
  return areas;
}
