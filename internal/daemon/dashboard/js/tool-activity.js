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

export function lineCounts(before, after) {
  const a = textLines(before);
  const b = textLines(after);
  if (a.length * b.length > 250000) return { added: b.length, removed: a.length };
  let previous = new Array(b.length + 1).fill(0);
  for (let i = 1; i <= a.length; i += 1) {
    const current = new Array(b.length + 1).fill(0);
    for (let j = 1; j <= b.length; j += 1) {
      current[j] = a[i - 1] === b[j - 1] ? previous[j - 1] + 1 : Math.max(previous[j], current[j - 1]);
    }
    previous = current;
  }
  const common = previous[b.length];
  return { added: b.length - common, removed: a.length - common };
}

export function patchChanges(patch) {
  const changes = [];
  for (const line of textLines(patch)) {
    const file = line.match(/^\*\*\* (?:Update|Add|Delete) File: (.+)$/);
    if (file) changes.push({ path: file[1].trim(), added: 0, removed: 0 });
    else if (line.startsWith("*** ") || !changes.length) continue;
    else if (line.startsWith("+")) changes[changes.length - 1].added += 1;
    else if (line.startsWith("-")) changes[changes.length - 1].removed += 1;
  }
  return changes;
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

export function toolLine(call) {
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
  if (name === "apply_patch") return { verb, object: toolChanges(call).map((change) => baseName(change.path)).join(", "), code: true };
  if (string(input?.pattern) !== undefined) return { verb, object: input.pattern, code: true };
  const path = string(input?.file_path) ?? string(input?.path) ?? string(input?.notebook_path);
  if (path !== undefined) return { verb, object: baseName(path), code: true };
  const summary = firstLine(toolSummary(name, text));
  return { verb, object: summary === "No input parameters" ? "" : summary, code: false };
}

export function toolChanges(call) {
  const name = call.title || "";
  const shape = content(name, call.text || "");
  if (shape.kind === "edit") return [{ path: shape.path, ...lineCounts(shape.before, shape.after) }];
  if (shape.kind !== "raw") return [];
  const input = toolInput(call.text);
  const path = string(input?.file_path) ?? string(input?.path);
  if (name === "apply_patch") return patchChanges(string(input?.input) ?? string(input?.patch) ?? call.text);
  if (["Write", "write_file"].includes(name) && path !== undefined && string(input.content) !== undefined) return [{ path, ...lineCounts("", input.content) }];
  if (name === "MultiEdit" && path !== undefined && Array.isArray(input.edits)) {
    return [input.edits.reduce((total, edit) => {
      const counts = lineCounts(string(edit?.old_string) ?? "", string(edit?.new_string) ?? "");
      return { path, added: total.added + counts.added, removed: total.removed + counts.removed };
    }, { path, added: 0, removed: 0 })];
  }
  return [];
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
