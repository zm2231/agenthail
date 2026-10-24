const rank = (item) => {
  const classes = item.className.split(" ");
  if (classes.includes("usage-problem")) return null;
  const critical = classes.includes("critical") ? 2 : 0;
  return critical + (classes.includes("usage-meter") ? 1 : 0);
};

export function dropOrder(items) {
  return items
    .map((item, index) => ({ item, index, rank: rank(item) }))
    .filter((entry) => entry.rank !== null)
    .sort((a, b) => a.rank - b.rank || b.index - a.index)
    .map((entry) => entry.item);
}

export function fit(line, overflows) {
  const items = [...line.children];
  for (const item of items) item.hidden = false;
  for (const item of dropOrder(items)) {
    if (!overflows(line)) return;
    item.hidden = true;
  }
}

export const wrapped = (line) => line.scrollHeight > line.clientHeight + 1;
