import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";

const source = fs.readFileSync(new URL("../internal/daemon/dashboard/app.js", import.meta.url), "utf8");
const start = source.indexOf("function sharedPeers");
const end = source.indexOf("function conversationMeta", start);
assert(start >= 0 && end > start, "shared conversation renderer boundary missing");
const escapeStart = source.indexOf("globalThis.escape =");
const escapeEnd = source.indexOf(");\n", escapeStart) + 3;
assert(escapeStart >= 0 && escapeEnd > escapeStart, "escape helper missing");

const note = { innerHTML: "", hidden: true };
const context = vm.createContext({ $: (selector) => ({ "#chat-shared": note })[selector] });
vm.runInContext(`${source.slice(escapeStart, escapeEnd)}\n${source.slice(start, end)}`, context);

const first = { id: "first", pid: 4101, status: "idle", startedAt: "2026-01-02T15:04:00Z" };
const second = { id: "second", pid: 4102, status: "busy", startedAt: "2026-01-02T15:09:00Z" };

assert.equal(context.sharedBadge({ id: "solo" }), "");
assert.equal(context.sharedBadge({ id: "solo", sharedWith: [] }), "");
const badge = context.sharedBadge({ id: "third", sharedWith: [first] });
assert.match(badge, /class="shared-badge"/);
assert.match(badge, />⧉2</);
assert.match(badge, /aria-label="Open in 2 processes"/);
assert.match(context.sharedBadge({ id: "third", sharedWith: [first, second] }), />⧉3</);

context.renderSharedNote({ id: "third", sharedWith: [first] });
assert.equal(note.hidden, false);
assert.match(note.innerHTML, /Also open in pid 4101 \(started [^)]+\)\. Messages here go to this process\./);
assert.match(note.innerHTML, /data-session="first"[^>]*>Open other</);

const rendered = note.innerHTML;
note.innerHTML = "sentinel";
context.renderSharedNote({ id: "third", sharedWith: [first] });
assert.equal(note.innerHTML, "sentinel", "an unchanged note must not be rewritten on each poll");
note.innerHTML = rendered;

context.renderSharedNote({ id: "third", sharedWith: [first, second] });
assert.match(note.innerHTML, /Also open in 2 other processes\. Messages here go to this process\./);
assert.match(note.innerHTML, /pid 4101 \(started [^)]+\)<\/span><button[^>]*data-session="first"/);
assert.match(note.innerHTML, /pid 4102 \(started [^)]+\)<\/span><button[^>]*data-session="second"/);

context.renderSharedNote({ id: "third", sharedWith: [{ id: "x", pid: 7, startedAt: "" }] });
assert.match(note.innerHTML, /pid 7 \(start time unknown\)/);

context.renderSharedNote({ id: "solo" });
assert.equal(note.hidden, true);
assert.equal(note.innerHTML, "");

console.log("Shared conversation: badge counts, header note, multi-process list and unshared rows pass.");
