export const lanes = [
  ["you", "Waiting on you"],
  ["ciFailing", "CI failing"],
  ["ciPending", "Waiting on CI"],
  ["review", "In review"],
  ["blocked", "Blocked"],
  ["running", "Working now"],
  ["idle", "Held, not running"],
];

const holderWords = { working: "working", idle: "idle", ended: "ended" };
const checkWords = { failing: "checks failing", pending: "checks running", passing: "checks pass" };
const reviewWords = { approved: "approved", changes_requested: "changes asked" };
const endedWords = { merged: "merged", closed: "closed" };

export function holderWord(flight) {
  return holderWords[flight?.holderState] || "";
}

export function prName(pr) {
  return pr.number ? `PR #${pr.number}` : "PR";
}

export function prWords(pr) {
  if (endedWords[pr.state]) return endedWords[pr.state];
  return [checkWords[pr.checks], reviewWords[pr.review]].filter(Boolean).join(", ");
}

export function prChip(flight) {
  const prs = flight?.prs || [];
  const pr = prs.find((each) => each.state === "open") || prs.at(-1);
  if (!pr) return null;
  const words = endedWords[pr.state] || checkWords[pr.checks] || reviewWords[pr.review] || "";
  const tone = pr.state === "merged" ? "good" : pr.state === "closed" ? "quiet" : pr.checks === "failing" ? "bad" : pr.checks === "pending" ? "warn" : pr.checks === "passing" ? "good" : "quiet";
  return { text: [prName(pr), words].filter(Boolean).join(" "), tone };
}

export function laneSince(lane, row) {
  if (lane === "you") {
    const asked = (row.flight?.asks || []).map((ask) => ask.since).filter(Boolean).sort();
    if (asked.length) return asked[0];
  }
  return row.since;
}
