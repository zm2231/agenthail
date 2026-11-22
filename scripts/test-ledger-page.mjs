import "./test-home.mjs";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const source = readFileSync(new URL("../internal/daemon/ledger_page/ledger-text.js", import.meta.url), "utf8");
const { withoutRefs, readable, shownRows, shortList } = await import("data:text/javascript," + encodeURIComponent(source));

assert.equal(withoutRefs("done: #1009 (54ee919f1)"), "", "a note that is only refs says nothing");
assert.equal(withoutRefs("merged #1009 (54ee919f1)"), "", "a status word left alone says nothing");
assert.equal(withoutRefs("Merged in #1009 (54ee919f1), the tiles keep their order"), "Merged, the tiles keep their order");
assert.equal(withoutRefs("Fixed in PR #1086 and 6c63b4554"), "");
assert.equal(withoutRefs("Superseded by #1070; the list drops ids"), "Superseded; the list drops ids");
assert.equal(withoutRefs("Keep the tile order in the daemon (#1086)"), "Keep the tile order in the daemon");
assert.equal(withoutRefs("Shipped: the reconnect test passes"), "the reconnect test passes");
assert.equal(withoutRefs("Retry after 2 KiB, decade, facade and the 1234567 bytes"), "Retry after 2 KiB, decade, facade and the 1234567 bytes", "plain words and numbers are not refs");
assert.equal(withoutRefs("Reverted 6C63B4554 after review"), "Reverted after review", "an uppercase hash is a ref too");
const titles = new Map([["agenthail-W12", "Keep the tile order"]]);
assert.equal(readable("Follows agenthail-W12 and agenthail-W9", titles), "Follows “Keep the tile order” and another item", "a known item reads as its title and an unknown one names no id");
assert.equal(readable("done: #1009 (54ee919f1) for agenthail-W12", titles), "for “Keep the tile order”");
const nested = new Map([["demo-W1", "Follow demo-W2"], ["demo-W2", "Lead demo-W1"]]);
assert.equal(readable("Depends on demo-W1", nested), "Depends on “Follow another item”", "an id inside a title is named as no id, and two titles naming each other end");
assert.equal(readable("See demo-W3", nested), "See another item");
assert.equal(withoutRefs(""), "");
assert.equal(withoutRefs(undefined), undefined);

const rows = Array.from({ length: 14 }, (_, n) => n);
assert.equal(shortList, 5);
assert.deepEqual(shownRows(rows, false), [0, 1, 2, 3, 4], "a section shows its first five");
assert.equal(shownRows(rows, true).length, 14, "and all of them once asked");
assert.deepEqual(shownRows([1, 2], false), [1, 2]);

const boardSource = readFileSync(new URL("../internal/daemon/ledger_page/ledger-board.js", import.meta.url), "utf8");
const { lanes, prChip, prWords, laneSince, holderWord } = await import("data:text/javascript," + encodeURIComponent(boardSource));
assert.deepEqual(lanes.map(([lane]) => lane), ["you", "ciFailing", "ciPending", "review", "blocked", "running", "idle"], "the page shows the lanes in the order the daemon picks them");
const failing = { url: "u7", number: 7, state: "open", checks: "failing", review: "none" };
const merged = { url: "u6", number: 6, state: "merged", checks: "failing" };
assert.deepEqual(prChip({ prs: [merged, failing] }), { text: "PR #7 checks failing", tone: "bad" }, "the card names the open PR over an older merged one");
assert.deepEqual(prChip({ prs: [merged] }), { text: "PR #6 merged", tone: "good" }, "a merged PR reads merged whatever its last checks were");
assert.deepEqual(prChip({ prs: [{ url: "u", state: "open" }] }), { text: "PR", tone: "quiet" }, "a PR not read yet reads as a bare PR");
assert.equal(prChip({ prs: [] }), null);
assert.equal(prChip(undefined), null);
assert.equal(prWords({ state: "open", checks: "passing", review: "approved" }), "checks pass, approved");
assert.equal(prWords({ state: "closed", checks: "failing" }), "closed");
assert.equal(laneSince("you", { since: "2026-11-01T00:00:00Z", flight: { asks: [{ since: "2026-11-21T00:00:00Z" }, { since: "2026-11-20T00:00:00Z" }] } }), "2026-11-20T00:00:00Z", "a card waiting on the person dates from its oldest ask");
assert.equal(laneSince("running", { since: "2026-11-01T00:00:00Z", flight: { asks: [] } }), "2026-11-01T00:00:00Z");
assert.equal(holderWord({ holderState: "working" }), "working");
assert.equal(holderWord({ holderState: "unknown" }), "", "a holder this host cannot see gets no word");
console.log("ledger page text ok");
