import { toolRowKey, toolRows } from "./tool-view.js";
import { tools } from "./tool-loader.js";
import { escape } from "./format.js";

export const toolBlocks = new Map();

export function rerenderToolBlock(block) {
  const render = toolBlocks.get(block?.dataset.block);
  if (render) block.outerHTML = render();
}

export const toolDiffCache = new Map();

export function cachedToolDiffs(item, session) {
  const key = toolRowKey(session, item.id);
  const cached = toolDiffCache.get(key);
  if (cached?.title === item.title && cached.text === item.text) return cached.diffs;
  const diffs = tools.toolDiffs(item);
  toolDiffCache.set(key, { title: item.title, text: item.text, diffs });
  return diffs;
}

export const diffPage = 400;

export function renderDiff(key, hunks) {
  toolBlocks.set(key, () => renderDiff(key, hunks));
  const entries = tools.diffEntries(hunks);
  const shown = Math.max(toolRows.get(key) || diffPage, diffPage);
  const rows = entries.slice(0, shown).map((entry) => {
    if (entry.hunk) return '<div class="diff-note">⋯</div>';
    if (entry.folded) return `<div class="diff-note">${entry.folded === 1 ? "1 unchanged line" : `${entry.folded} unchanged lines`}</div>`;
    const spoken = { added: "Added: ", removed: "Removed: ", same: "" }[entry.kind];
    return `<div class="diff-line ${entry.kind}"><span class="diff-sign" aria-hidden="true">${{ added: "+", removed: "−", same: " " }[entry.kind]}</span><span class="diff-text">${spoken ? `<span class="sr-only">${spoken}</span>` : ""}${markedText(entry.text, entry.marks)}</span></div>`;
  }).join("");
  const more = entries.length > shown ? `<button class="tool-control diff-more" type="button" data-diff-show="${shown + diffPage}">Show ${Math.min(diffPage, entries.length - shown)} more lines</button>` : "";
  return `<div class="tool-diff" data-block="${escape(key)}">${rows}${more}</div>`;
}

export function markedText(text, marks) {
  if (!marks.length) return escape(text);
  const characters = tools.characters(text);
  let html = "";
  let at = 0;
  for (const [start, end] of marks) {
    html += `${escape(characters.slice(at, start).join(""))}<mark>${escape(characters.slice(start, end).join(""))}</mark>`;
    at = end;
  }
  return html + escape(characters.slice(at).join(""));
}

export function renderFileChips(key, diffs) {
  toolBlocks.set(key, () => renderFileChips(key, diffs));
  const isOpen = (diff) => toolRows.get(`${key}:${diff.path}`) === true;
  const buttons = diffs.map((diff) => {
    const change = tools.fileChange(diff);
    const name = tools.baseName(change.path);
    return `<button class="file-chip${isOpen(diff) ? " open" : ""}" type="button" data-chip="${escape(change.path)}" aria-expanded="${isOpen(diff)}" title="${escape(change.path)}" aria-label="${escape(`${name}, ${change.added} added, ${change.removed} removed`)}"><span>${escape(name)}</span>${change.added ? `<span class="added">+${change.added}</span>` : ""}${change.removed ? `<span class="removed">−${change.removed}</span>` : ""}</button>`;
  }).join("");
  const opened = diffs.filter(isOpen).map((diff) => `${diffs.length > 1 ? `<div class="diff-file">${escape(tools.baseName(diff.path))}</div>` : ""}${renderDiff(`${key}:${diff.path}:diff`, diff.hunks)}`).join("");
  return `<div class="file-chips" data-block="${escape(key)}"><div class="file-chip-list">${buttons}</div>${opened}</div>`;
}

export function toggleFileChip(button) {
  const block = button.closest(".file-chips[data-block]");
  if (!block) return;
  const key = `${block.dataset.block}:${button.dataset.chip}`;
  toolRows.set(key, toolRows.get(key) !== true);
  rerenderToolBlock(block);
}

export function showToolDiff(button) {
  const block = button.closest(".tool-diff[data-block]");
  if (!block) return;
  toolRows.set(block.dataset.block, Number(button.dataset.diffShow) || diffPage);
  rerenderToolBlock(block);
}
