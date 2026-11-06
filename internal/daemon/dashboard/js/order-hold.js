export function bindOrderHold(list, { onRelease, settle = 3000, root = document, setTimeout = globalThis.setTimeout, clearTimeout = globalThis.clearTimeout }) {
  let holding = false;
  let down = false;
  let inside = false;
  let settled = true;
  let timer = null;
  const release = () => {
    if (!holding || down || (inside && !settled)) return;
    holding = false;
    onRelease();
  };
  const moved = () => {
    inside = true;
    holding = true;
    settled = false;
    clearTimeout(timer);
    timer = setTimeout(() => { settled = true; release(); }, settle);
  };
  list.addEventListener("pointermove", moved);
  list.addEventListener("scroll", moved, { passive: true });
  list.addEventListener("pointerdown", () => {
    down = true;
    moved();
  });
  const up = () => {
    if (!down) return;
    down = false;
    release();
  };
  root.addEventListener("pointerup", up);
  root.addEventListener("pointercancel", up);
  list.addEventListener("pointerleave", () => {
    inside = false;
    settled = true;
    clearTimeout(timer);
    release();
  });
  return () => holding;
}
