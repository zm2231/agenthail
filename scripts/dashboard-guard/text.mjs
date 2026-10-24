export function collectText() {
  const parseColour = (value) => {
    const match = /rgba?\(([^)]+)\)/.exec(value);
    if (!match) return null;
    const parts = match[1].split(/[\s,/]+/).filter(Boolean).map(Number);
    return [parts[0] / 255, parts[1] / 255, parts[2] / 255, parts.length > 3 ? parts[3] : 1];
  };
  const opacity = (element) => {
    let total = 1;
    for (let node = element; node; node = node.parentElement) total *= Number(getComputedStyle(node).opacity);
    return total;
  };
  const path = (element) => {
    const parts = [];
    for (let node = element; node && node !== document.body && parts.length < 4; node = node.parentElement) {
      parts.unshift(node.id ? `#${node.id}` : `${node.tagName.toLowerCase()}${node.classList.length ? "." + [...node.classList].join(".") : ""}`);
    }
    return parts.join(" > ");
  };
  const clipFor = (element) => {
    let clip = [0, 0, innerWidth, innerHeight];
    for (let node = element; node; node = node.parentElement) {
      const style = getComputedStyle(node);
      if (style.position === "absolute" && style.clip !== "auto") return [0, 0, 0, 0];
      if (node === element || (style.overflowX === "visible" && style.overflowY === "visible" && style.clipPath === "none")) continue;
      const box = node.getBoundingClientRect();
      clip = [Math.max(clip[0], box.left), Math.max(clip[1], box.top), Math.min(clip[2], box.right), Math.min(clip[3], box.bottom)];
    }
    return clip;
  };
  const CELL = 12;
  const cellsOf = (rect, clip, pane) => {
    const left = Math.max(rect.left, clip[0]);
    const top = Math.max(rect.top, clip[1]);
    const right = Math.min(rect.right, clip[2]);
    const bottom = Math.min(rect.bottom, clip[3]);
    const cells = [];
    for (let y = top; y < bottom; y += CELL) {
      for (let x = left; x < right; x += CELL) {
        const w = Math.min(CELL, right - x);
        const h = Math.min(CELL, bottom - y);
        const topmost = document.elementsFromPoint(x + w / 2, y + h / 2).find((hit) => hit.matches("[data-pane], [data-readable]"));
        if (!pane || topmost === pane) cells.push([x, y, w, h]);
      }
    }
    return cells;
  };
  const out = [];
  const range = document.createRange();
  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const text = node.textContent.trim();
    const element = node.parentElement;
    if (!text || !element || element.closest(".ui-backdrop, script, style, template, noscript")) continue;
    if (!element.checkVisibility({ opacityProperty: true, visibilityProperty: true, contentVisibilityAuto: true })) continue;
    range.selectNodeContents(node);
    const clip = clipFor(element);
    const pane = element.closest("[data-pane], [data-readable]");
    const shown = [...range.getClientRects()].filter((r) => r.width > 1 && r.height > 1 && r.right > clip[0] + 1 && r.left < clip[2] - 1 && r.bottom > clip[1] + 1 && r.top < clip[3] - 1);
    const cells = shown.flatMap((rect) => cellsOf(rect, clip, pane));
    if (!cells.length) continue;
    const style = getComputedStyle(element);
    const box = pane?.getBoundingClientRect();
    out.push({
      text: text.slice(0, 60),
      where: path(element),
      pane: pane ? path(pane) : null,
      inside: box ? cells.every(([x, y, w, h]) => x >= box.left - 1 && x + w <= box.right + 1 && y >= box.top - 1 && y + h <= box.bottom + 1) : false,
      inactive: Boolean(element.closest(":disabled, [aria-disabled='true']")),
      colour: parseColour(style.webkitTextFillColor || style.color),
      opacity: opacity(element),
      rects: cells,
    });
  }
  return out;
}

export const HIDE_TEXT = `*, *::before, *::after { color: transparent !important; -webkit-text-fill-color: transparent !important; text-shadow: none !important; caret-color: transparent !important; text-decoration-color: transparent !important; transition: none !important; }`;
