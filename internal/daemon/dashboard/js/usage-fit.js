const rank = (item) => {
  const classes = item.className.split(" ");
  if (!classes.includes("usage-chip") || classes.includes("critical") || classes.includes("problem")) return null;
  return 0;
};

export function dropOrder(items) {
  return items
    .map((item, index) => ({ item, index, rank: rank(item) }))
    .filter((entry) => entry.rank !== null)
    .sort((a, b) => b.index - a.index)
    .map((entry) => entry.item);
}

export function fit(line, overflows) {
  const items = [...line.children];
  const more = items.find((item) => item.className === "usage-more");
  for (const item of items) item.hidden = item === more;
  line.removeAttribute("data-compact");
  if (!overflows(line)) return;
  line.setAttribute("data-compact", "");
  let dropped = 0;
  for (const item of dropOrder(items)) {
    if (!overflows(line)) break;
    item.hidden = true;
    dropped += 1;
    if (more) {
      more.textContent = items.every((other) => other === more || other.hidden) ? `${dropped} accounts` : `+${dropped}`;
      more.hidden = false;
    }
  }
}

export const wrapped = (line) => line.scrollHeight > line.clientHeight + 1;
