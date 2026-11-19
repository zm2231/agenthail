export const shortList = 5;

const pullRequest = /(?:\bPR\s*)?\(?#\d+\)?/gi;
const commit = /\(?\b(?=[0-9a-f]*\d)(?=[0-9a-f]*[a-f])[0-9a-f]{7,40}\b\)?/gi;
const workID = /\b[A-Za-z0-9][A-Za-z0-9-]*-W\d+\b/g;

const status = /^(?:done|merged|shipped|fixed|landed|closed)$/i;
const dangling = /\s+\b(?:in|as|via|by|and|with|at|on)\b(?=\s*(?:[,.;]|$))/gi;

export function withoutRefs(text) {
  if (!text) return text;
  let out = text
    .replace(pullRequest, "")
    .replace(commit, "")
    .replace(/\(\s*[,;]?\s*\)/g, "")
    .replace(/^\s*(?:done|merged|shipped|fixed|landed|closed)\s*:\s*/i, "")
    .replace(/\s+([,.;:)])/g, "$1")
    .replace(/\s{2,}/g, " ");
  let before;
  do {
    before = out;
    out = out.replace(dangling, "").replace(/([,;:])(?=\s*(?:[,;:.]|$))/g, "").trim();
  } while (out !== before);
  return status.test(out) ? "" : out;
}

const unnamed = "another item";

export function readable(text, titles) {
  if (!text) return text;
  const title = (id) => (titles.has(id) ? `“${titles.get(id).replace(workID, unnamed)}”` : unnamed);
  return withoutRefs(text.replace(workID, title));
}

export function shownRows(rows, all) {
  return all ? rows : rows.slice(0, shortList);
}
