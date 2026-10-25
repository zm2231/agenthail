// Mirrors native/ToolPresentation.swift. scripts/fixtures/tool-activity-vectors.json
// holds what the Swift produces; scripts/test-dashboard-tool-activity.mjs checks
// this file against it, so change both together.

export function toolInput(text) {
  try {
    const value = JSON.parse(text || "");
    return value && typeof value === "object" && !Array.isArray(value) ? value : null;
  } catch {
    return null;
  }
}

const string = (value) => (typeof value === "string" ? value : undefined);
const blank = (text) => !String(text).trim();

export function baseName(path) {
  return String(path || "").split("/").filter(Boolean).pop() || String(path || "");
}

export function firstLine(text) {
  return String(text || "").split(/\r\n|\n/).find((line) => line !== "") || "";
}

export function textLines(text) {
  const lines = String(text ?? "").split(/\r\n|\n/);
  if (lines[lines.length - 1] === "") lines.pop();
  return lines;
}

function content(name, text) {
  const input = toolInput(text);
  if (!input) return { kind: "raw" };
  const command = string(input.command) ?? string(input.cmd);
  if (command !== undefined) return { kind: "command", command };
  const path = string(input.file_path) ?? string(input.path);
  const before = string(input.old_string) ?? string(input.oldText);
  const after = string(input.new_string) ?? string(input.newText);
  if (path !== undefined && before !== undefined && after !== undefined) return { kind: "edit", path, before, after };
  const steps = Array.isArray(input.todos) ? input.todos : Array.isArray(input.plan) ? input.plan : null;
  if ((name === "TodoWrite" || name.endsWith("update_plan")) && steps?.every((step) => step && typeof step === "object" && !Array.isArray(step))) {
    return { kind: "plan", steps: steps.map((step) => string(step.content) ?? string(step.step) ?? "") };
  }
  return { kind: "raw" };
}

const summaryKeys = ["description", "file_path", "path", "pattern", "query", "url", "prompt", "subject", "message", "title", "name"];

export function toolSummary(name, text) {
  const shape = content(name, text);
  if (shape.kind === "command") return shape.command;
  if (shape.kind === "edit") return shape.path;
  if (shape.kind === "plan") return shape.steps.join(" · ");
  const input = toolInput(text);
  if (!input) return text ? text : "No input parameters";
  if (name === "TaskUpdate" && string(input.taskId) !== undefined) {
    return [`Task ${input.taskId}`, string(input.status)?.replaceAll("_", " ")].filter((part) => part !== undefined).join(" · ");
  }
  if (name === "TaskCreate" && string(input.subject)) return input.subject;
  for (const key of [...summaryKeys, ...Object.keys(input).sort()]) {
    const value = string(input[key]);
    if (value !== undefined && !blank(value)) return value;
  }
  const count = Object.keys(input).length;
  return count === 0 ? "No input parameters" : count === 1 ? "1 field" : `${count} fields`;
}

function title(name) {
  const titles = { Read: "Read file", read_file: "Read file", Write: "Write file", write_file: "Write file", Grep: "Search files", Glob: "Search files", WebSearch: "Search the web", WebFetch: "Open webpage", Agent: "Delegate to agent", Task: "Delegate to agent", TaskCreate: "Add task", TaskUpdate: "Update task" };
  return titles[name] ?? name.replaceAll("functions.", "").replaceAll("mcp__", "").replaceAll("__", " · ").replaceAll("_", " ");
}

const verbs = { Read: "Read", read_file: "Read", NotebookRead: "Read", view_image: "Read", Write: "Write", write_file: "Write", MultiEdit: "Edit", NotebookEdit: "Edit", apply_patch: "Edit", Grep: "Search", Glob: "Search", LS: "Search", ToolSearch: "Search", WebSearch: "Search the web", web_search: "Search the web", WebFetch: "Open", Agent: "Delegate", Task: "Delegate", spawn_agent: "Delegate" };

// `diffs` takes the call's toolDiffs when the caller already has them.
export function toolLine(call, diffs) {
  const name = call.title || call.kind || "Tool";
  const text = call.text || "";
  const shape = content(name, text);
  const input = toolInput(text);
  if (shape.kind === "command") {
    const intent = !["TaskCreate", "TaskUpdate"].includes(name) && string(input.description) !== undefined && !blank(input.description) ? input.description : undefined;
    return intent !== undefined ? { verb: "Run", object: intent, code: false } : { verb: "Run", object: firstLine(shape.command), code: true };
  }
  if (shape.kind === "edit") return { verb: "Edit", object: baseName(shape.path), code: true };
  if (shape.kind === "plan") return { verb: "Update plan", object: "", code: false };
  const verb = verbs[name] ?? title(name);
  if (name === "apply_patch") return { verb, object: (diffs ?? toolDiffs(call)).map((diff) => baseName(diff.path)).join(", "), code: true };
  if (string(input?.pattern) !== undefined) return { verb, object: input.pattern, code: true };
  const path = string(input?.file_path) ?? string(input?.path) ?? string(input?.notebook_path);
  if (path !== undefined) return { verb, object: baseName(path), code: true };
  const summary = firstLine(toolSummary(name, text));
  return { verb, object: summary === "No input parameters" ? "" : summary, code: false };
}

export function toolDiffs(call) {
  const name = call.title || "";
  const shape = content(name, call.text || "");
  if (shape.kind === "edit") return [{ path: shape.path, hunks: [textDiff(shape.before, shape.after)] }];
  if (shape.kind !== "raw") return [];
  const input = toolInput(call.text);
  const path = string(input?.file_path) ?? string(input?.path);
  if (["Write", "write_file"].includes(name)) return path !== undefined && string(input.content) !== undefined ? [{ path, hunks: [textDiff("", input.content)] }] : [];
  if (name === "MultiEdit") {
    const edits = input?.edits;
    if (path === undefined || !Array.isArray(edits) || !edits.every((edit) => edit && typeof edit === "object" && !Array.isArray(edit))) return [];
    return [{ path, hunks: edits.map((edit) => textDiff(string(edit.old_string) ?? "", string(edit.new_string) ?? "")) }];
  }
  if (name === "apply_patch") return patchDiffs(string(input?.input) ?? string(input?.patch) ?? call.text);
  return [];
}

export function toolChanges(call) {
  return toolDiffs(call).map(fileChange);
}

export function readableInput(text) {
  const input = toolInput(text);
  if (!input || !Object.keys(input).length) return text || "";
  const commandKey = ["command", "cmd"].find((key) => typeof input[key] === "string");
  const lines = commandKey ? [`$ ${input[commandKey]}`] : [];
  for (const key of Object.keys(input).sort()) {
    if (key === commandKey) continue;
    const value = input[key];
    lines.push(`${key}: ${typeof value === "string" ? value : json(value, "")}`);
  }
  return lines.join("\n");
}

function json(value, indent) {
  const inner = `${indent}  `;
  if (typeof value === "string") return JSON.stringify(value);
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  if (Array.isArray(value)) return value.length ? `[\n${value.map((item) => inner + json(item, inner)).join(",\n")}\n${indent}]` : "[]";
  if (value && typeof value === "object") {
    const keys = Object.keys(value).sort();
    return keys.length ? `{\n${keys.map((key) => `${inner}${JSON.stringify(key)}: ${json(value[key], inner)}`).join(",\n")}\n${indent}}` : "{}";
  }
  return "null";
}

const callKinds = ["toolCall", "tool", "command"];
const interruptedStatuses = ["cancelled", "canceled", "interrupted", "task_cancelled", "task_canceled", "task_aborted", "turn_aborted"];
const failedStatuses = ["failed", "error", "systemerror"];
export const callStateLabels = {
  running: "Running",
  interrupted: "Interrupted before a result",
  failed: "The turn failed before a result",
  unrecorded: "No result recorded",
  later: "Result recorded further down",
};

// A call whose turn has no recorded end reads as running only while its
// session is active; it is never called interrupted on a guess.
export function toolCallStates(items, active) {
  const states = new Map();
  let open = [];
  const end = (state, ends) => {
    for (const call of open) if (ends(call)) states.set(call.id, state);
    open = open.filter((call) => !ends(call));
  };
  for (const item of items) {
    if (callKinds.includes(item.kind)) {
      open.push(item);
    } else if (item.kind === "toolResult" && item.callId) {
      end("later", (call) => call.callId === item.callId);
    } else if (item.kind === "done" || (item.kind === "event" && item.title === "Turn interrupted")) {
      const status = item.status || "";
      const state = item.kind === "event" || interruptedStatuses.includes(status) ? "interrupted" : failedStatuses.includes(status) ? "failed" : "unrecorded";
      end(state, (call) => !call.turnId || !item.turnId || call.turnId === item.turnId);
    } else if (item.turnId) {
      end("unrecorded", (call) => call.turnId && call.turnId !== item.turnId);
    }
  }
  for (const call of open) states.set(call.id, active ? "running" : "unrecorded");
  return states;
}

// Output lines as ToolRunSummary splits them: "\r\n" is one Swift character, so it does not end a line.
export function outputLines(text) {
  return String(text ?? "").split(/(?<!\r)\n/);
}

const graphemes = new Intl.Segmenter(undefined, { granularity: "grapheme" });

// The first 24 lines, then at most 3000 characters, as ToolRunSummary.outputPreview.
export function outputPreview(text) {
  const head = outputLines(text).slice(0, 24).join("\n");
  let preview = "";
  let count = 0;
  for (const { segment } of graphemes.segment(head)) {
    if (count === 3000) break;
    preview += segment;
    count += 1;
  }
  return preview;
}

// Lines each "Show more" adds, as the Mac's ToolOutputBlock.page.
export const outputPage = 500;

// Mirrors native/TextDiff.swift: Alignment, TextDiff and FileDiff, and DiffEntry.entries for the rows.
export const cellLimit = 4000000;

export function alignmentSteps(old, next, limit = cellLimit) {
  let prefix = 0;
  while (prefix < old.length && prefix < next.length && old[prefix] === next[prefix]) prefix += 1;
  let suffix = 0;
  while (suffix < old.length - prefix && suffix < next.length - prefix && old[old.length - 1 - suffix] === next[next.length - 1 - suffix]) suffix += 1;
  const a = old.slice(prefix, old.length - suffix);
  const b = next.slice(prefix, next.length - suffix);
  const n = a.length;
  const m = b.length;
  const steps = new Array(prefix).fill("same");
  if (n * m > limit) {
    steps.push(...new Array(n).fill("removed"), ...new Array(m).fill("added"));
  } else {
    const common = new Int32Array((n + 1) * (m + 1));
    for (let i = n - 1; i >= 0; i -= 1) {
      for (let j = m - 1; j >= 0; j -= 1) {
        common[i * (m + 1) + j] = a[i] === b[j] ? common[(i + 1) * (m + 1) + j + 1] + 1 : Math.max(common[(i + 1) * (m + 1) + j], common[i * (m + 1) + j + 1]);
      }
    }
    let i = 0;
    let j = 0;
    while (i < n || j < m) {
      if (i < n && j < m && a[i] === b[j]) {
        steps.push("same");
        i += 1;
        j += 1;
      } else if (i < n && (j === m || common[(i + 1) * (m + 1) + j] >= common[i * (m + 1) + j + 1])) {
        steps.push("removed");
        i += 1;
      } else {
        steps.push("added");
        j += 1;
      }
    }
  }
  return steps.concat(new Array(suffix).fill("same"));
}

export function textDiff(before, after) {
  const old = textLines(before);
  const next = textLines(after);
  let i = 0;
  let j = 0;
  return alignmentSteps(old, next).map((kind) => {
    if (kind === "removed") return { kind, text: old[i++], marks: [] };
    if (kind === "added") return { kind, text: next[j++], marks: [] };
    i += 1;
    return { kind, text: next[j++], marks: [] };
  });
}

export function patchDiffs(text) {
  const files = [];
  const append = (kind, line) => {
    const hunks = files[files.length - 1].hunks;
    if (!hunks.length) hunks.push([]);
    hunks[hunks.length - 1].push({ kind, text: line.slice(1), marks: [] });
  };
  for (const line of textLines(text)) {
    const file = ["*** Update File: ", "*** Add File: ", "*** Delete File: "].find((prefix) => line.startsWith(prefix));
    if (file) files.push({ path: line.slice(file.length).trim(), hunks: [] });
    else if (line.startsWith("*** ") || !files.length) continue;
    else if (line.startsWith("@@")) { const hunks = files[files.length - 1].hunks; if (hunks.length && hunks[hunks.length - 1].length) hunks.push([]); }
    else if (line.startsWith("+")) append("added", line);
    else if (line.startsWith("-")) append("removed", line);
    else if (line.startsWith(" ")) append("same", line);
  }
  return files.map((file) => ({ path: file.path, hunks: file.hunks.filter((hunk) => hunk.length) }));
}

export function fileChange(diff) {
  const lines = diff.hunks.flat();
  return { path: diff.path, added: lines.filter((line) => line.kind === "added").length, removed: lines.filter((line) => line.kind === "removed").length };
}

// One entry per file, in first-touched order, with the hunks of every call that changed it.
export function mergedDiffs(diffs) {
  const merged = [];
  for (const diff of diffs) {
    const existing = merged.find((other) => other.path === diff.path);
    if (existing) existing.hunks.push(...diff.hunks);
    else merged.push({ path: diff.path, hunks: [...diff.hunks] });
  }
  return merged;
}

export const markLimit = 1000;
const pairSimilarity = 0.25;
const pairWindow = 8;

export function characters(text) {
  return Array.from(graphemes.segment(text), ({ segment }) => segment);
}

function wordKind(character) {
  const scalar = String.fromCodePoint(character.codePointAt(0));
  if (/\p{Alphabetic}|\p{N}/u.test(scalar) || scalar === "_") return 0;
  if (/\p{White_Space}/u.test(scalar)) return 1;
  return 2;
}

function words(text) {
  const result = [];
  let current = [];
  let start = 0;
  let offset = 0;
  for (const character of characters(text)) {
    const last = current[current.length - 1];
    if (last !== undefined && (wordKind(last) !== wordKind(character) || wordKind(character) === 2)) {
      result.push({ text: current.join(""), start, end: offset });
      current = [];
      start = offset;
    }
    current.push(character);
    offset += 1;
  }
  if (current.length) result.push({ text: current.join(""), start, end: offset });
  return result;
}

function ranges(list, changed) {
  const result = [];
  for (const index of [...changed].sort((x, y) => x - y)) {
    const last = result[result.length - 1];
    if (last && last[1] === list[index].start) last[1] = list[index].end;
    else result.push([list[index].start, list[index].end]);
  }
  return result;
}

// The words that differ between two lines, in character offsets, and how much of the longer line they share.
export function lineMarks(old, next) {
  const oldCharacters = characters(old);
  const nextCharacters = characters(next);
  const visible = Math.max(oldCharacters.filter((character) => wordKind(character) !== 1).length, nextCharacters.filter((character) => wordKind(character) !== 1).length);
  if (oldCharacters.length > markLimit || nextCharacters.length > markLimit || visible === 0) return { old: [], next: [], similarity: 0 };
  const oldWords = words(old);
  const nextWords = words(next);
  const removed = new Set();
  const inserted = new Set();
  let i = 0;
  let j = 0;
  for (const step of alignmentSteps(oldWords.map((word) => word.text), nextWords.map((word) => word.text))) {
    if (step === "removed") removed.add(i);
    if (step === "added") inserted.add(j);
    if (step !== "added") i += 1;
    if (step !== "removed") j += 1;
  }
  const common = oldWords.reduce((total, word, index) => (removed.has(index) || characters(word.text).every((character) => wordKind(character) === 1) ? total : total + word.end - word.start), 0);
  return { old: ranges(oldWords, removed), next: ranges(nextWords, inserted), similarity: common / visible };
}

function marked(lines) {
  const result = lines.map((line) => ({ ...line, marks: [] }));
  let index = 0;
  while (index < result.length) {
    if (result[index].kind !== "removed") {
      index += 1;
      continue;
    }
    let removedEnd = index;
    while (removedEnd < result.length && result[removedEnd].kind === "removed") removedEnd += 1;
    let addedEnd = removedEnd;
    while (addedEnd < result.length && result[addedEnd].kind === "added") addedEnd += 1;
    let next = removedEnd;
    for (let old = index; old < removedEnd && next < addedEnd; old += 1) {
      let best = null;
      for (let candidate = next; candidate < Math.min(addedEnd, next + pairWindow); candidate += 1) {
        const marks = lineMarks(result[old].text, result[candidate].text);
        if (marks.similarity >= pairSimilarity && (!best || marks.similarity > best.marks.similarity)) best = { candidate, marks };
      }
      if (!best) continue;
      result[old].marks = best.marks.old;
      result[best.candidate].marks = best.marks.next;
      next = best.candidate + 1;
    }
    index = addedEnd;
  }
  return result;
}

// Changes with `context` unchanged lines around each, the words that changed marked; longer unchanged stretches fold.
export function diffRows(lines, context = 3) {
  const all = marked(lines);
  const rows = [];
  let index = 0;
  while (index < all.length) {
    if (all[index].kind !== "same") {
      rows.push(all[index]);
      index += 1;
      continue;
    }
    let end = index;
    while (end < all.length && all[end].kind === "same") end += 1;
    const leading = index === 0 ? 0 : context;
    const trailing = end === all.length ? 0 : context;
    if (end - index > leading + trailing + 1) {
      rows.push(...all.slice(index, index + leading), { folded: end - index - leading - trailing }, ...all.slice(end - trailing, end));
    } else {
      rows.push(...all.slice(index, end));
    }
    index = end;
  }
  return rows;
}

// A file's hunks as rows, with a break between hunks, as DiffEntry.entries.
export function diffEntries(hunks) {
  return hunks.flatMap((hunk, index) => [...(index > 0 ? [{ hunk: true }] : []), ...diffRows(hunk)]);
}
