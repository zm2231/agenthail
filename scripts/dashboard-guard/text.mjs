export function collectText() {
  const paint = new OffscreenCanvas(1, 1).getContext("2d", { willReadFrequently: true });
  const parseColour = (value) => {
    paint.clearRect(0, 0, 1, 1);
    paint.fillStyle = "#000";
    paint.fillStyle = value;
    paint.fillRect(0, 0, 1, 1);
    const [r, g, b, a] = paint.getImageData(0, 0, 1, 1).data;
    return a ? [r / 255, g / 255, b / 255, a / 255] : [0, 0, 0, 0];
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
  const panes = [...document.querySelectorAll("[data-pane], [data-readable]")].map((element) => {
    const box = element.getBoundingClientRect();
    return { element, box: [box.left, box.top, box.right, box.bottom] };
  });
  const overlap = (a, b) => [Math.max(a[0], b[0]), Math.max(a[1], b[1]), Math.min(a[2], b[2]), Math.min(a[3], b[3])];
  const empty = (r) => r[2] - r[0] < 0.5 || r[3] - r[1] < 0.5;
  const occluders = new Map();
  const above = (pane) => {
    if (occluders.has(pane)) return occluders.get(pane);
    const own = panes.find((entry) => entry.element === pane);
    const out = [];
    for (const other of panes) {
      if (other.element === pane || other.element.contains(pane)) continue;
      const shared = overlap(own.box, other.box);
      if (empty(shared)) continue;
      const hits = document.elementsFromPoint((shared[0] + shared[2]) / 2, (shared[1] + shared[3]) / 2);
      const mine = hits.findIndex((hit) => hit.closest("[data-pane], [data-readable]") === pane);
      const theirs = hits.findIndex((hit) => other.element.contains(hit));
      if (theirs !== -1 && (mine === -1 || theirs < mine)) out.push(shared);
    }
    occluders.set(pane, out);
    return out;
  };
  const subtract = (pieces, cut) =>
    pieces.flatMap((r) => {
      const shared = overlap(r, cut);
      if (empty(shared)) return [r];
      return [
        [r[0], r[1], r[2], shared[1]],
        [r[0], shared[3], r[2], r[3]],
        [r[0], shared[1], shared[0], shared[3]],
        [shared[2], shared[1], r[2], shared[3]],
      ].filter((part) => !empty(part));
    });
  const cellsOf = (rect, clip, pane) => {
    let pieces = [overlap([rect.left, rect.top, rect.right, rect.bottom], clip)].filter((part) => !empty(part));
    if (pane) for (const cut of above(pane)) pieces = subtract(pieces, cut);
    const cells = [];
    for (const [left, top, right, bottom] of pieces) {
      for (let y = top; y < bottom; y += CELL) {
        for (let x = left; x < right; x += CELL) cells.push([x, y, Math.min(CELL, right - x), Math.min(CELL, bottom - y)]);
      }
    }
    return cells;
  };
  const out = [];
  const modal = document.querySelector(":modal");
  const range = document.createRange();
  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const text = node.textContent.trim();
    const element = node.parentElement;
    if (!text || !element || element.closest(".ui-backdrop, script, style, template, noscript")) continue;
    if (modal && !modal.contains(element)) continue;
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
