export const PANES = [
  ["chrome", ".topbar"],
  ["sidebar", ".conversation-rail"],
  ["content", ".notebook, .settings-card"],
  ["float", ".composer form, .slash-menu, .voice-dialog, .toast, .project-menu-items, .host-picker-menu"],
];
export const READABLE = "[data-readable]";
const PANE_SELECTOR = PANES.map(([, selector]) => selector).join(", ");

function shown(element, view) {
  const rect = element.getBoundingClientRect();
  if (rect.width <= 0 || rect.height <= 0) return null;
  return view.getComputedStyle(element).visibility === "hidden" ? null : rect;
}

function anchor(element, view) {
  for (let node = element; node && node.nodeType === 1; node = node.parentElement) {
    const style = view.getComputedStyle(node);
    if (style.position === "fixed" || style.position === "sticky") return { node, position: style.position, top: parseFloat(style.top) || 0 };
  }
  return null;
}

export function reach(rect, anchored, scrollY, maxScroll) {
  if (anchored?.position === "fixed") return [rect.top, rect.bottom];
  if (anchored) {
    const base = anchored.rect.top + scrollY;
    const offset = rect.top - anchored.rect.top;
    return [base - maxScroll + offset, Math.max(base, anchored.top) + offset + rect.height];
  }
  const top = rect.top + scrollY;
  return [top - maxScroll, top + rect.height];
}

export function collectRegions(document, view) {
  const regions = [];
  const maxScroll = Math.max(document.documentElement.scrollHeight - view.innerHeight, 0);
  const add = (element, kind, pane) => {
    const rect = shown(element, view);
    if (!rect) return;
    const left = Math.max(0, rect.left);
    const right = Math.min(view.innerWidth, rect.right);
    const found = anchor(element, view);
    const anchored = found && { ...found, rect: found.node.getBoundingClientRect() };
    const [low, high] = reach(rect, anchored, view.scrollY, maxScroll);
    const top = Math.max(0, low);
    const bottom = Math.min(view.innerHeight, high);
    if (right <= left || bottom <= top) return;
    regions.push({ id: String(regions.length), element, kind, pane, rect: [left, top, right - left, bottom - top].map(Math.round) });
  };
  for (const [pane, selector] of PANES) for (const element of document.querySelectorAll(selector)) add(element, "pane", pane);
  for (const element of document.querySelectorAll(READABLE)) if (!element.closest(PANE_SELECTOR)) add(element, "text");
  return regions;
}

export function regionKey(regions) {
  return regions.map(({ kind, pane, rect }) => `${kind}:${pane || ""}:${rect.join(",")}`).join("|");
}

function clipped(element, view) {
  for (let node = element; node && node.nodeType === 1; node = node.parentElement) {
    const style = view.getComputedStyle(node);
    if (style.clip !== "auto" && style.clip !== "") return true;
    if (style.overflow === "hidden") {
      const rect = node.getBoundingClientRect();
      if (rect.width <= 1 || rect.height <= 1) return true;
    }
  }
  return false;
}

export function uncoveredText(document, view) {
  const walker = document.createTreeWalker(document.body, view.NodeFilter.SHOW_TEXT);
  const range = document.createRange();
  const out = [];
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const text = node.textContent.trim();
    const element = node.parentElement;
    if (!text || !element || element.closest(".ui-backdrop, script, style, template")) continue;
    range.selectNodeContents(node);
    const visible = [...range.getClientRects()].some((rect) => rect.width > 1 && rect.height > 1);
    if (!visible || view.getComputedStyle(element).visibility === "hidden" || clipped(element, view)) continue;
    if (element.closest(PANE_SELECTOR) || element.closest(READABLE)) continue;
    out.push(`${text.slice(0, 60)} (${element.tagName.toLowerCase()}${element.className ? "." + String(element.className).split(" ").join(".") : ""})`);
  }
  return out;
}
